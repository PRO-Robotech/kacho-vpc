package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/outbox"
	"github.com/PRO-Robotech/kacho-corelib/selector"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"

	commonv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/common/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const networkColumns = `uid, folder_id, cloud_id, organization_id, name, labels, annotations,
	creation_timestamp, resource_version, generation, deletion_timestamp, finalizers, spec, status`

// NetworkRepo реализует service.NetworkRepo.
type NetworkRepo struct {
	pool         *pgxpool.Pool
	transactor   *coredb.Transactor
	outboxWriter *outbox.Writer
}

// NewNetworkRepo создаёт NetworkRepo.
func NewNetworkRepo(pool *pgxpool.Pool, transactor *coredb.Transactor, outboxWriter *outbox.Writer) *NetworkRepo {
	return &NetworkRepo{pool: pool, transactor: transactor, outboxWriter: outboxWriter}
}

func (r *NetworkRepo) GetByUID(ctx context.Context, uid string) (*domain.Network, error) {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+networkColumns+` FROM networks WHERE uid = $1 AND deletion_timestamp IS NULL`, pgUID)
	return scanNetwork(row)
}

func (r *NetworkRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Network, error) {
	pgFolderID, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+networkColumns+` FROM networks WHERE folder_id = $1 AND name = $2 AND deletion_timestamp IS NULL`,
		pgFolderID, name)
	return scanNetwork(row)
}

func (r *NetworkRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.Network, string, int64, error) {
	var coreSelectors []selector.Selector
	for _, s := range selectors {
		cs := selector.Selector{Labels: s.Labels}
		if s.Name != "" || s.FolderID != "" || s.CloudID != "" || s.OrganizationID != "" {
			cs.Field = &selector.FieldFilter{
				Name:           s.Name,
				FolderID:       s.FolderID,
				CloudID:        s.CloudID,
				OrganizationID: s.OrganizationID,
			}
		}
		coreSelectors = append(coreSelectors, cs)
	}

	pageSize := int(page.PageSize)
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 100
	}

	snapshotRV, err := r.SnapshotResourceVersion(ctx)
	if err != nil {
		return nil, "", 0, err
	}

	br, err := selector.Build(coreSelectors)
	if err != nil {
		return nil, "", 0, err
	}

	var pageClause string
	var pageArgs []any
	var pageToken *selector.PageToken

	if page.PageToken != "" {
		var tok selector.PageToken
		if jsonErr := json.Unmarshal([]byte(page.PageToken), &tok); jsonErr == nil {
			pageToken = &tok
		}
	}

	paramBase := len(br.Args) + 1
	if pageToken != nil {
		pageClause, pageArgs = selector.BuildPageClause(pageToken, paramBase)
		paramBase += len(pageArgs)
	}

	query := buildListQueryNetwork(br, pageClause)
	args := append(br.Args, pageArgs...)
	args = append(args, pageSize+1)
	limitParam := fmt.Sprintf("$%d", paramBase)
	query += fmt.Sprintf(" ORDER BY resource_version ASC, uid ASC LIMIT %s", limitParam)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()

	var networks []*domain.Network
	for rows.Next() {
		n, scanErr := scanNetworkFromRows(rows)
		if scanErr != nil {
			return nil, "", 0, scanErr
		}
		networks = append(networks, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, err
	}

	var nextToken string
	if len(networks) > pageSize {
		networks = networks[:pageSize]
		last := networks[len(networks)-1]
		tok := selector.PageToken{LastResourceVersion: last.ResourceVersion, LastUID: last.UID}
		b, _ := json.Marshal(tok)
		nextToken = string(b)
	}

	return networks, nextToken, snapshotRV, nil
}

func (r *NetworkRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	var rv int64
	err := r.pool.QueryRow(ctx, `SELECT last_value FROM resource_version_seq`).Scan(&rv)
	return rv, err
}

func (r *NetworkRepo) Insert(ctx context.Context, net *domain.Network) (*domain.Network, error) {
	pgUID, err := strToUUID(net.UID)
	if err != nil {
		return nil, err
	}
	pgFolderID, err := strToUUID(net.FolderID)
	if err != nil {
		return nil, err
	}
	pgCloudID, err := strToUUID(net.CloudID)
	if err != nil {
		// CloudID может быть пустым для первых тестов — допускаем nil UUID
		pgCloudID = pgtype.UUID{}
	}
	pgOrgID, err := strToUUID(net.OrganizationID)
	if err != nil {
		pgOrgID = pgtype.UUID{}
	}

	var result *domain.Network
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`INSERT INTO networks (uid, folder_id, cloud_id, organization_id, name, labels, annotations, spec, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			 RETURNING `+networkColumns,
			pgUID, pgFolderID, pgCloudID, pgOrgID, net.Name,
			mapToJSONB(net.Labels), mapToJSONB(net.Annotations),
			domainNetworkToSpec(net), domainNetworkToStatus(net),
		)
		n, scanErr := scanNetwork(row)
		if scanErr != nil {
			return scanErr
		}
		result = n

		data, _ := proto.Marshal(domainNetworkToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "ADDED",
			ResourceKind: "Network",
			ResourceUID:  result.UID,
			Data:         data,
		})
		return evtErr
	})
	if txErr != nil {
		return nil, txErr
	}
	_ = r.outboxWriter.Notify(ctx, r.pool)
	return result, nil
}

func (r *NetworkRepo) Update(ctx context.Context, net *domain.Network) (*domain.Network, error) {
	pgUID, err := strToUUID(net.UID)
	if err != nil {
		return nil, err
	}

	var result *domain.Network
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`UPDATE networks SET labels = $2, annotations = $3, spec = $4, generation = generation + 1
			 WHERE uid = $1 RETURNING `+networkColumns,
			pgUID, mapToJSONB(net.Labels), mapToJSONB(net.Annotations), domainNetworkToSpec(net),
		)
		n, scanErr := scanNetwork(row)
		if scanErr != nil {
			return scanErr
		}
		result = n

		data, _ := proto.Marshal(domainNetworkToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "MODIFIED",
			ResourceKind: "Network",
			ResourceUID:  result.UID,
			Data:         data,
		})
		return evtErr
	})
	if txErr != nil {
		return nil, txErr
	}
	_ = r.outboxWriter.Notify(ctx, r.pool)
	return result, nil
}

func (r *NetworkRepo) HardDelete(ctx context.Context, uid string) error {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return err
	}
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		_, delErr := tx.Exec(ctx, `DELETE FROM networks WHERE uid = $1`, pgUID)
		if delErr != nil {
			return delErr
		}
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "DELETED",
			ResourceKind: "Network",
			ResourceUID:  uid,
			Data:         nil,
		})
		return evtErr
	})
	if txErr != nil {
		return txErr
	}
	_ = r.outboxWriter.Notify(ctx, r.pool)
	return nil
}

func (r *NetworkRepo) HasDependents(ctx context.Context, uid string) (bool, error) {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return false, err
	}
	var count int
	err = r.pool.QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM subnets WHERE network_id = $1 AND deletion_timestamp IS NULL) +
		        (SELECT COUNT(*) FROM security_groups WHERE network_id = $1 AND deletion_timestamp IS NULL) +
		        (SELECT COUNT(*) FROM route_tables WHERE network_id = $1 AND deletion_timestamp IS NULL)`,
		pgUID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func scanNetwork(row pgx.Row) (*domain.Network, error) {
	var (
		uid               pgtype.UUID
		folderID          pgtype.UUID
		cloudID           pgtype.UUID
		orgID             pgtype.UUID
		name              string
		labels            []byte
		annotations       []byte
		creationTimestamp pgtype.Timestamptz
		resourceVersion   int64
		generation        int64
		deletionTimestamp pgtype.Timestamptz
		finalizers        []string
		spec              []byte
		statusBytes       []byte
	)
	err := row.Scan(&uid, &folderID, &cloudID, &orgID, &name, &labels, &annotations,
		&creationTimestamp, &resourceVersion, &generation, &deletionTimestamp, &finalizers, &spec, &statusBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	var specData networkSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData networkStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	return &domain.Network{
		UID:               uuidToStr(uid),
		FolderID:          uuidToStr(folderID),
		CloudID:           uuidToStr(cloudID),
		OrganizationID:    uuidToStr(orgID),
		Name:              name,
		Labels:            jsonbToMap(labels),
		Annotations:       jsonbToMap(annotations),
		CreationTimestamp: tsToTime(creationTimestamp),
		ResourceVersion:   resourceVersion,
		Generation:        generation,
		DeletionTimestamp: tsToTimePtr(deletionTimestamp),
		Finalizers:        finalizers,
		DisplayName:       specData.DisplayName,
		Description:       specData.Description,
		State:             statusData.State,
	}, nil
}

func scanNetworkFromRows(rows pgx.Rows) (*domain.Network, error) {
	var (
		uid               pgtype.UUID
		folderID          pgtype.UUID
		cloudID           pgtype.UUID
		orgID             pgtype.UUID
		name              string
		labels            []byte
		annotations       []byte
		creationTimestamp pgtype.Timestamptz
		resourceVersion   int64
		generation        int64
		deletionTimestamp pgtype.Timestamptz
		finalizers        []string
		spec              []byte
		statusBytes       []byte
	)
	err := rows.Scan(&uid, &folderID, &cloudID, &orgID, &name, &labels, &annotations,
		&creationTimestamp, &resourceVersion, &generation, &deletionTimestamp, &finalizers, &spec, &statusBytes)
	if err != nil {
		return nil, err
	}

	var specData networkSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData networkStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	return &domain.Network{
		UID:               uuidToStr(uid),
		FolderID:          uuidToStr(folderID),
		CloudID:           uuidToStr(cloudID),
		OrganizationID:    uuidToStr(orgID),
		Name:              name,
		Labels:            jsonbToMap(labels),
		Annotations:       jsonbToMap(annotations),
		CreationTimestamp: tsToTime(creationTimestamp),
		ResourceVersion:   resourceVersion,
		Generation:        generation,
		DeletionTimestamp: tsToTimePtr(deletionTimestamp),
		Finalizers:        finalizers,
		DisplayName:       specData.DisplayName,
		Description:       specData.Description,
		State:             statusData.State,
	}, nil
}

func buildListQueryNetwork(br selector.BuildResult, pageClause string) string {
	var sb strings.Builder
	sb.WriteString(`SELECT ` + networkColumns + ` FROM networks WHERE deletion_timestamp IS NULL`)

	if br.WhereClause != "" {
		clause := strings.TrimPrefix(br.WhereClause, "WHERE ")
		sb.WriteString(" AND (")
		sb.WriteString(clause)
		sb.WriteString(")")
	}

	if pageClause != "" {
		sb.WriteString(" AND (")
		sb.WriteString(pageClause)
		sb.WriteString(")")
	}

	return sb.String()
}

func domainNetworkToProto(n *domain.Network) *pb.Network {
	meta := &commonv1.ResourceMeta{
		Uid:             n.UID,
		Name:            n.Name,
		FolderId:        n.FolderID,
		CloudId:         n.CloudID,
		OrganizationId:  n.OrganizationID,
		Labels:          n.Labels,
		Annotations:     n.Annotations,
		ResourceVersion: fmt.Sprintf("%d", n.ResourceVersion),
		Generation:      n.Generation,
		Finalizers:      n.Finalizers,
	}
	if !n.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(n.CreationTimestamp)
	}
	if n.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*n.DeletionTimestamp)
	}
	return &pb.Network{
		Metadata: meta,
		Spec:     &pb.NetworkSpec{DisplayName: n.DisplayName, Description: n.Description},
		Status:   &pb.NetworkStatus{State: n.State},
	}
}
