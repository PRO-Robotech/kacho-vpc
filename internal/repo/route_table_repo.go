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
	"google.golang.org/protobuf/types/known/timestamppb"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/outbox"
	"github.com/PRO-Robotech/kacho-corelib/selector"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"

	commonv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/common/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

const rtColumns = `uid, network_id, folder_id, cloud_id, organization_id, name, labels, annotations,
	creation_timestamp, resource_version, generation, deletion_timestamp, finalizers, spec, status`

// RouteTableRepo реализует service.RouteTableRepo.
type RouteTableRepo struct {
	pool         *pgxpool.Pool
	transactor   *coredb.Transactor
	outboxWriter *outbox.Writer
}

// NewRouteTableRepo создаёт RouteTableRepo.
func NewRouteTableRepo(pool *pgxpool.Pool, transactor *coredb.Transactor, outboxWriter *outbox.Writer) *RouteTableRepo {
	return &RouteTableRepo{pool: pool, transactor: transactor, outboxWriter: outboxWriter}
}

func (r *RouteTableRepo) GetByUID(ctx context.Context, uid string) (*domain.RouteTable, error) {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+rtColumns+` FROM route_tables WHERE uid = $1 AND deletion_timestamp IS NULL`, pgUID)
	return scanRT(row)
}

func (r *RouteTableRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.RouteTable, error) {
	pgFolderID, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+rtColumns+` FROM route_tables WHERE folder_id = $1 AND name = $2 AND deletion_timestamp IS NULL`,
		pgFolderID, name)
	return scanRT(row)
}

func (r *RouteTableRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.RouteTable, string, int64, error) {
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

	query := buildListQueryRT(br, pageClause)
	args := append(br.Args, pageArgs...)
	args = append(args, pageSize+1)
	limitParam := fmt.Sprintf("$%d", paramBase)
	query += fmt.Sprintf(" ORDER BY resource_version ASC, uid ASC LIMIT %s", limitParam)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()

	var rts []*domain.RouteTable
	for rows.Next() {
		rt, scanErr := scanRTFromRows(rows)
		if scanErr != nil {
			return nil, "", 0, scanErr
		}
		rts = append(rts, rt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, err
	}

	var nextToken string
	if len(rts) > pageSize {
		rts = rts[:pageSize]
		last := rts[len(rts)-1]
		tok := selector.PageToken{LastResourceVersion: last.ResourceVersion, LastUID: last.UID}
		b, _ := json.Marshal(tok)
		nextToken = string(b)
	}
	return rts, nextToken, snapshotRV, nil
}

func (r *RouteTableRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	var rv int64
	err := r.pool.QueryRow(ctx, `SELECT last_value FROM resource_version_seq`).Scan(&rv)
	return rv, err
}

func (r *RouteTableRepo) Insert(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	pgUID, err := strToUUID(rt.UID)
	if err != nil {
		return nil, err
	}
	pgNetID, err := strToUUID(rt.NetworkID)
	if err != nil {
		return nil, err
	}
	pgFolderID, err := strToUUID(rt.FolderID)
	if err != nil {
		return nil, err
	}
	pgCloudID, _ := strToUUID(rt.CloudID)
	pgOrgID, _ := strToUUID(rt.OrganizationID)

	var result *domain.RouteTable
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`INSERT INTO route_tables (uid, network_id, folder_id, cloud_id, organization_id, name, labels, annotations, spec, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 RETURNING `+rtColumns,
			pgUID, pgNetID, pgFolderID, pgCloudID, pgOrgID, rt.Name,
			mapToJSONB(rt.Labels), mapToJSONB(rt.Annotations),
			domainRTToSpec(rt), domainRTToStatus(rt),
		)
		res, scanErr := scanRT(row)
		if scanErr != nil {
			return scanErr
		}
		result = res

		data, _ := proto.Marshal(domainRTToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "ADDED",
			ResourceKind: "RouteTable",
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

func (r *RouteTableRepo) Update(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	pgUID, err := strToUUID(rt.UID)
	if err != nil {
		return nil, err
	}

	var result *domain.RouteTable
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`UPDATE route_tables SET labels = $2, annotations = $3, spec = $4, generation = generation + 1
			 WHERE uid = $1 RETURNING `+rtColumns,
			pgUID, mapToJSONB(rt.Labels), mapToJSONB(rt.Annotations), domainRTToSpec(rt),
		)
		res, scanErr := scanRT(row)
		if scanErr != nil {
			return scanErr
		}
		result = res

		data, _ := proto.Marshal(domainRTToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "MODIFIED",
			ResourceKind: "RouteTable",
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

func (r *RouteTableRepo) HardDelete(ctx context.Context, uid string) error {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return err
	}
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		_, delErr := tx.Exec(ctx, `DELETE FROM route_tables WHERE uid = $1`, pgUID)
		if delErr != nil {
			return delErr
		}
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "DELETED",
			ResourceKind: "RouteTable",
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

func (r *RouteTableRepo) HasDependents(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func scanRT(row pgx.Row) (*domain.RouteTable, error) {
	var (
		uid               pgtype.UUID
		networkID         pgtype.UUID
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
	err := row.Scan(&uid, &networkID, &folderID, &cloudID, &orgID, &name, &labels, &annotations,
		&creationTimestamp, &resourceVersion, &generation, &deletionTimestamp, &finalizers, &spec, &statusBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	var specData rtSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData rtStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	return &domain.RouteTable{
		UID:               uuidToStr(uid),
		NetworkID:         uuidToStr(networkID),
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
		StaticRoutes:      specToStaticRoutes(spec),
		State:             statusData.State,
	}, nil
}

func scanRTFromRows(rows pgx.Rows) (*domain.RouteTable, error) {
	var (
		uid               pgtype.UUID
		networkID         pgtype.UUID
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
	err := rows.Scan(&uid, &networkID, &folderID, &cloudID, &orgID, &name, &labels, &annotations,
		&creationTimestamp, &resourceVersion, &generation, &deletionTimestamp, &finalizers, &spec, &statusBytes)
	if err != nil {
		return nil, err
	}

	var specData rtSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData rtStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	return &domain.RouteTable{
		UID:               uuidToStr(uid),
		NetworkID:         uuidToStr(networkID),
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
		StaticRoutes:      specToStaticRoutes(spec),
		State:             statusData.State,
	}, nil
}

func buildListQueryRT(br selector.BuildResult, pageClause string) string {
	var sb strings.Builder
	sb.WriteString(`SELECT ` + rtColumns + ` FROM route_tables WHERE deletion_timestamp IS NULL`)
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

func domainRTToProto(rt *domain.RouteTable) *pb.RouteTable {
	meta := &commonv1.ResourceMeta{
		Uid:             rt.UID,
		Name:            rt.Name,
		FolderId:        rt.FolderID,
		CloudId:         rt.CloudID,
		OrganizationId:  rt.OrganizationID,
		Labels:          rt.Labels,
		Annotations:     rt.Annotations,
		ResourceVersion: fmt.Sprintf("%d", rt.ResourceVersion),
		Generation:      rt.Generation,
		Finalizers:      rt.Finalizers,
	}
	if !rt.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(rt.CreationTimestamp)
	}
	if rt.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*rt.DeletionTimestamp)
	}

	routes := make([]*pb.StaticRoute, len(rt.StaticRoutes))
	for i, r := range rt.StaticRoutes {
		routes[i] = &pb.StaticRoute{
			Id:                r.ID,
			DestinationPrefix: r.DestinationPrefix,
			NextHopAddress:    r.NextHopAddress,
			Description:       r.Description,
		}
	}

	return &pb.RouteTable{
		Metadata: meta,
		Spec: &pb.RouteTableSpec{
			NetworkId:    rt.NetworkID,
			DisplayName:  rt.DisplayName,
			Description:  rt.Description,
			StaticRoutes: routes,
		},
		Status: &pb.RouteTableStatus{State: rt.State},
	}
}
