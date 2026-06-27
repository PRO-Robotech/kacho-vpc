// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// Проверяет cross-service revoke прав по labels: Network.Update обязан повторно
// эмитить mirror-feed RegisterResource (labels + parent_project_id) при смене
// labels — в ТОЙ ЖЕ writer-tx, что и UPDATE ресурса, через существующий
// fga_register_outbox (без dual-write).
//
// Это testcontainers integration-тесты, гоняющие реальный Network use-case
// (Handler → UpdateNetworkUseCase → kachopg writer-tx) на Postgres 16 и
// проверяющие строки fga_register_outbox, которые позже применяет IAM
// register-drainer. resource_mirror живет в kacho-iam; здесь наблюдаем только
// consumer-side intent.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	networkapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/network"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// ---- helpers ----

// newNetworkHandler собирает реальный Network Handler поверх testcontainers-пула
// (kachopg writer-tx), детерминированного in-memory OpsRepo (AwaitOpDone) и
// always-OK project client. defaultSGInline=false → Network.Create эмитит ровно
// один register intent (tuple сети), поэтому число строк на сеть тривиально
// проверяется.
func newNetworkHandler(t *testing.T, pool *pgxpool.Pool) (*networkapp.Handler, *repomock.OpsRepo) {
	t.Helper()
	r := kachopg.New(pool, nil)
	t.Cleanup(r.Close)
	or := repomock.NewOpsRepo()
	pc := &repomock.ProjectClient{OK: true}

	create := networkapp.NewCreateNetworkUseCase(r, pc, or, false)
	update := networkapp.NewUpdateNetworkUseCase(r, or)
	// Read/list/delete use-cases здесь не задействованы, но нужны NewHandler.
	get := networkapp.NewGetNetworkUseCase(r, nil)
	list := networkapp.NewListNetworksUseCase(r, nil)
	listSub := networkapp.NewListSubnetsUseCase(r, nil)
	listSG := networkapp.NewListSecurityGroupsUseCase(r, nil)
	listRT := networkapp.NewListRouteTablesUseCase(r, nil)
	listOps := networkapp.NewListOperationsUseCase(or)
	del := networkapp.NewDeleteNetworkUseCase(r, nil, nil, nil, or)
	h := networkapp.NewHandler(create, update, del, get, list, listSub, listSG, listRT, listOps)
	return h, or
}

// createNetworkVia вызывает Network.Create, дожидается операции и возвращает новый id.
func createNetworkVia(t *testing.T, h *networkapp.Handler, or *repomock.OpsRepo, projectID, name string, labels map[string]string) string {
	t.Helper()
	ctx := context.Background()
	op, err := h.Create(ctx, &vpcv1.CreateNetworkRequest{
		ProjectId: projectID,
		Name:      name,
		Labels:    labels,
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error, "Create op must succeed")

	resp, err := h.List(ctx, &vpcv1.ListNetworksRequest{ProjectId: projectID})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Networks)
	return resp.Networks[0].Id
}

// registerPayloads возвращает payloads register-событий (fga.register) из outbox
// для данной сети, oldest-first, декодированные из JSONB-колонки.
func registerPayloads(ctx context.Context, t *testing.T, pool *pgxpool.Pool, netID string) []fgaregister.Payload {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT payload FROM kacho_vpc.fga_register_outbox
		   WHERE resource_id = $1 AND event_type = 'fga.register' ORDER BY id ASC`, netID)
	require.NoError(t, err)
	defer rows.Close()
	var out []fgaregister.Payload
	for rows.Next() {
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var p fgaregister.Payload
		require.NoError(t, json.Unmarshal(raw, &p))
		out = append(out, p)
	}
	require.NoError(t, rows.Err())
	return out
}

// ---- Удаление label повторно эмитит mirror.upsert ----

// TestNetworkRepo_T31Revoke01_LabelRemoveEmitsMirrorUpsert — очистка label через
// Update(labels={}) обязана эмитить второй register intent с пустыми labels
// (upsert с {}, НЕ Unregister) в той же writer-tx, что и UPDATE.
func TestNetworkRepo_T31Revoke01_LabelRemoveEmitsMirrorUpsert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h, or := newNetworkHandler(t, pool)

	netID := createNetworkVia(t, h, or, "prj-A", "net-treska", map[string]string{"network": "treska"})

	op, err := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
		NetworkId:  netID,
		Labels:     map[string]string{},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)

	regs := registerPayloads(ctx, t, pool, netID)
	require.Len(t, regs, 2, "Create + Update(labels={}) → два register intent")
	// upsert с пустыми labels, parent сохранен, НЕ unregister.
	assert.Empty(t, regs[1].Labels, "label-remove → пустые labels в повторно эмитированном intent")
	assert.Equal(t, "prj-A", regs[1].ParentProjectID, "parent_project_id сохраняется при label-remove")
	assert.Equal(t, "vpc_network:"+netID, regs[1].Tuple.Object)
	assert.Equal(t, "project", regs[1].Tuple.Relation, "relation project-hierarchy tuple")
	require.False(t, regs[1].SourceVersion.IsZero(), "source_version проставлен на Update intent")
	assert.LessOrEqual(t, regs[0].SourceVersion.UnixNano(), regs[1].SourceVersion.UnixNano(),
		"source_version монотонен Create→Update")
}

// ---- Добавление label материализует grant ----

func TestNetworkRepo_T31Add01_LabelAddMaterializesGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h, or := newNetworkHandler(t, pool)

	netID := createNetworkVia(t, h, or, "prj-A", "net-plain", nil)

	op, err := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
		NetworkId:  netID,
		Labels:     map[string]string{"network": "treska"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)

	regs := registerPayloads(ctx, t, pool, netID)
	require.Len(t, regs, 2, "Create(без labels) + Update(labels добавлены) → два intent")
	assert.Equal(t, map[string]string{"network": "treska"}, regs[1].Labels,
		"label-add → обновленные labels в повторно эмитированном intent (selector теперь matches)")
	assert.Equal(t, "prj-A", regs[1].ParentProjectID)
}

// ---- Смена label мигрирует grant ----

func TestNetworkRepo_T31Change01_LabelSwapMigratesGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h, or := newNetworkHandler(t, pool)

	netID := createNetworkVia(t, h, or, "prj-A", "net-x", map[string]string{"network": "treska"})

	op, err := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
		NetworkId:  netID,
		Labels:     map[string]string{"network": "okun"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)

	regs := registerPayloads(ctx, t, pool, netID)
	require.Len(t, regs, 2, "Create + Update(смена label) → два intent")
	assert.Equal(t, map[string]string{"network": "okun"}, regs[1].Labels,
		"label-swap → новый label в повторно эмитированном intent (grant мигрирует)")
}

// ---- Update без labels не эмитит лишний intent ----

func TestNetworkRepo_T31Idm01_NonLabelUpdateNoEmit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h, or := newNetworkHandler(t, pool)

	netID := createNetworkVia(t, h, or, "prj-A", "net-y", map[string]string{"network": "treska"})

	op, err := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
		NetworkId:   netID,
		Description: "renamed",
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"description"}},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)

	regs := registerPayloads(ctx, t, pool, netID)
	require.Len(t, regs, 1, "Update без labels → нет нового register intent (labels не в mask)")
}

// ---- Пустой update_mask (full PATCH) эмитит ----

func TestNetworkRepo_T31FullPatch01_EmptyMaskEmits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h, or := newNetworkHandler(t, pool)

	// Case A: full-PATCH обнуляет label → revoke.
	netID := createNetworkVia(t, h, or, "prj-A", "net-fp", map[string]string{"network": "treska"})
	op, err := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
		NetworkId: netID,
		// пустой update_mask = full-object PATCH; labels отсутствуют → {}.
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)
	regs := registerPayloads(ctx, t, pool, netID)
	require.Len(t, regs, 2, "пустой mask = full PATCH ⇒ labelsInMask=true ⇒ emit")
	assert.Empty(t, regs[1].Labels, "full-PATCH обнулил labels → intent с пустыми labels")

	// Case B: full-PATCH с labels в теле → emit с labels. Отдельный project, чтобы
	// List внутри createNetworkVia вернул net-fp2, а не старую net-fp.
	netID2 := createNetworkVia(t, h, or, "prj-B", "net-fp2", nil)
	op2, err := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
		NetworkId: netID2,
		Labels:    map[string]string{"network": "treska"},
	})
	require.NoError(t, err)
	saved2 := repomock.AwaitOpDone(t, or, op2.Id)
	require.True(t, saved2.Done)
	require.Nil(t, saved2.Error)
	regs2 := registerPayloads(ctx, t, pool, netID2)
	require.Len(t, regs2, 2, "пустой mask full PATCH с labels в теле ⇒ emit")
	assert.Equal(t, map[string]string{"network": "treska"}, regs2[1].Labels)
}

// ---- Rollback Update → нет intent (одна writer-tx) ----

// TestNetworkRepo_T31Atom01_RollbackNoIntent проверяет атомарность на уровне
// writer-tx: tx, аборченная после UPDATE ресурса + register emit, не коммитит
// НИЧЕГО — ни изменение строки, ни register intent. Это доказывает, что intent
// едет в ТОЙ ЖЕ tx (без dual-write): провалившийся Update не оставит orphan
// intent, а успешный — не потеряет его.
func TestNetworkRepo_T31Atom01_RollbackNoIntent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h, or := newNetworkHandler(t, pool)

	netID := createNetworkVia(t, h, or, "prj-A", "net-z", map[string]string{"network": "treska"})
	before := registerPayloads(ctx, t, pool, netID)
	require.Len(t, before, 1)

	// Гоним writer-tx, повторяющую network/update.go (UPDATE + register emit), и
	// делаем Abort вместо Commit — оба изменения должны откатиться вместе.
	r := kachopg.New(pool, nil)
	t.Cleanup(r.Close)
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	rec, err := w.Networks().GetForUpdate(ctx, netID)
	require.NoError(t, err)
	rec.Network.Labels = domain.LabelsFromMap(nil) // очищаем labels
	_, err = w.Networks().Update(ctx, &rec.Network)
	require.NoError(t, err)
	require.NoError(t, w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
		fgaregister.ProjectHierarchyItem("prj-A", "vpc_network", netID, nil),
	)))
	w.Abort() // имитируем сбой до commit

	// Ни UPDATE, ни register intent не пережили abort.
	after := registerPayloads(ctx, t, pool, netID)
	require.Len(t, after, 1, "аборченная writer-tx НЕ оставляет нового register intent")

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got, err := rd.Networks().Get(ctx, netID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	gotLabels := got.Labels
	v, ok := gotLabels.Get("network")
	require.True(t, ok, "смена label откатилась вместе с аборченной tx")
	assert.Equal(t, "treska", string(v))
}

// ---- Конкурентный flip label → last-source-wins, без stale ----

// TestNetworkRepo_T31Conc01_ConcurrentLabelFlip_LastSourceWins запускает ≥2
// goroutines, конкурентно флипающих label ({network:treska} ↔ {}), затем делает
// ОДИН финальный сериализованный Update к известному label. Каждый Update едет в
// своей writer-tx (row-lock от GetForUpdate сериализует RMW) и эмитит register
// intent атомарно с закоммиченной строкой, поэтому per-tx строка и ее intent
// никогда не расходятся. IAM применяет last-source-wins по source_version.
//
// Проверяем consumer-side инварианты, переживающие гонку:
//   - каждый эмитированный register intent — это состояние label, которое
//     реально было записано (нет torn/garbage payload), и labels согласованы со
//     строкой в той tx (intent не опережает ни одну закоммиченную строку);
//   - после финального детерминированного Update строка сети И intent с
//     максимальным source_version несут этот финальный label — mirror сходится к
//     финальному состоянию ресурса, stale-membership не зависает.
func TestNetworkRepo_T31Conc01_ConcurrentLabelFlip_LastSourceWins(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h, or := newNetworkHandler(t, pool)

	netID := createNetworkVia(t, h, or, "prj-A", "net-c", map[string]string{"network": "treska"})

	const n = 6
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			labels := map[string]string{"network": "treska"}
			if i%2 == 1 {
				labels = map[string]string{}
			}
			op, uerr := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
				NetworkId:  netID,
				Labels:     labels,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
			})
			if uerr != nil {
				return
			}
			repomock.AwaitOpDone(t, or, op.Id)
		}(i)
	}
	wg.Wait()

	// Финальный детерминированный Update — сериализован после шторма — задает winner.
	finOp, err := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
		NetworkId:  netID,
		Labels:     map[string]string{"network": "okun"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
	})
	require.NoError(t, err)
	finSaved := repomock.AwaitOpDone(t, or, finOp.Id)
	require.True(t, finSaved.Done)
	require.Nil(t, finSaved.Error)

	// Финальное состояние строки.
	r := kachopg.New(pool, nil)
	t.Cleanup(r.Close)
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got, err := rd.Networks().Get(ctx, netID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	v, ok := got.Labels.Get("network")
	require.True(t, ok, "финальная строка несет детерминированный winner-label")
	require.Equal(t, "okun", string(v))

	// Каждый register intent — одно из реально записанных состояний label
	// ({network:treska} | {} | {network:okun}), без torn/garbage payload.
	regs := registerPayloads(ctx, t, pool, netID)
	require.GreaterOrEqual(t, len(regs), 2, "intent'ы Create + шторм + финальный Update")
	for _, p := range regs {
		switch lv, has := p.Labels["network"]; {
		case !has:
			assert.Empty(t, p.Labels, "intent с очищенным label несет пустую map")
		default:
			assert.Contains(t, []string{"treska", "okun"}, lv, "label intent'а — реально записанное значение")
		}
	}

	// Intent с максимальным source_version зеркалит финальную строку
	// (сходимость last-source-wins — stale-membership не зависает).
	maxIdx := 0
	for i := 1; i < len(regs); i++ {
		if regs[i].SourceVersion.After(regs[maxIdx].SourceVersion) {
			maxIdx = i
		}
	}
	assert.Equal(t, "okun", regs[maxIdx].Labels["network"],
		"intent с max source_version зеркалит финальную строку сети (mirror сходится, last-source-wins)")
}

// ---- IAM недоступен → Update все равно коммитит, intent durable ----

// TestNetworkRepo_T31Unavail01_IamDown_IntentDurable проверяет, что mirror emit —
// это асинхронный outbox-relay, а НЕ синхронный precondition: даже без доступного
// IAM Network.Update коммитит (Operation done, без ошибки), а register intent
// durable ложится в fga_register_outbox с sent_at IS NULL — drainer повторит его
// позже (at-least-once), intent никогда не теряется. У Handler нет IAM-клиента на
// request-path, поэтому «IAM недоступен» моделируется отсутствием drainer'а:
// intent просто ждет в outbox.
func TestNetworkRepo_T31Unavail01_IamDown_IntentDurable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h, or := newNetworkHandler(t, pool)

	netID := createNetworkVia(t, h, or, "prj-A", "net-u", map[string]string{"network": "treska"})

	// Drainer не запущен (= IAM недоступен). Update все равно проходит.
	op, err := h.Update(ctx, &vpcv1.UpdateNetworkRequest{
		NetworkId:  netID,
		Labels:     map[string]string{},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	require.True(t, saved.Done, "Update коммитит независимо от доступности IAM")
	require.Nil(t, saved.Error, "mirror emit — async outbox, а не синхронный UNAVAILABLE precondition")

	// Intent durable в outbox, недоставлен (sent_at IS NULL).
	var undelivered int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM kacho_vpc.fga_register_outbox
		   WHERE resource_id = $1 AND event_type = 'fga.register' AND sent_at IS NULL`, netID).
		Scan(&undelivered))
	assert.GreaterOrEqual(t, undelivered, 2,
		"register intent'ы Create + Update оба durable и недоставлены (at-least-once, не теряются)")
}
