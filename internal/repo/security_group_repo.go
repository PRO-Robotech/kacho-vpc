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

const sgColumns = `uid, network_id, folder_id, cloud_id, organization_id, name, labels, annotations,
	creation_timestamp, resource_version, generation, deletion_timestamp, finalizers, spec, status`

// SecurityGroupRepo реализует service.SecurityGroupRepo.
type SecurityGroupRepo struct {
	pool         *pgxpool.Pool
	transactor   *coredb.Transactor
	outboxWriter *outbox.Writer
}

// NewSecurityGroupRepo создаёт SecurityGroupRepo.
func NewSecurityGroupRepo(pool *pgxpool.Pool, transactor *coredb.Transactor, outboxWriter *outbox.Writer) *SecurityGroupRepo {
	return &SecurityGroupRepo{pool: pool, transactor: transactor, outboxWriter: outboxWriter}
}

func (r *SecurityGroupRepo) GetByUID(ctx context.Context, uid string) (*domain.SecurityGroup, error) {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+sgColumns+` FROM security_groups WHERE uid = $1 AND deletion_timestamp IS NULL`, pgUID)
	return scanSG(row)
}

func (r *SecurityGroupRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.SecurityGroup, error) {
	pgFolderID, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+sgColumns+` FROM security_groups WHERE folder_id = $1 AND name = $2 AND deletion_timestamp IS NULL`,
		pgFolderID, name)
	return scanSG(row)
}

func (r *SecurityGroupRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.SecurityGroup, string, int64, error) {
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

	query := buildListQuerySG(br, pageClause)
	args := append(br.Args, pageArgs...)
	args = append(args, pageSize+1)
	limitParam := fmt.Sprintf("$%d", paramBase)
	query += fmt.Sprintf(" ORDER BY resource_version ASC, uid ASC LIMIT %s", limitParam)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()

	var sgs []*domain.SecurityGroup
	for rows.Next() {
		sg, scanErr := scanSGFromRows(rows)
		if scanErr != nil {
			return nil, "", 0, scanErr
		}
		sgs = append(sgs, sg)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, err
	}

	var nextToken string
	if len(sgs) > pageSize {
		sgs = sgs[:pageSize]
		last := sgs[len(sgs)-1]
		tok := selector.PageToken{LastResourceVersion: last.ResourceVersion, LastUID: last.UID}
		b, _ := json.Marshal(tok)
		nextToken = string(b)
	}
	return sgs, nextToken, snapshotRV, nil
}

func (r *SecurityGroupRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	var rv int64
	err := r.pool.QueryRow(ctx, `SELECT last_value FROM resource_version_seq`).Scan(&rv)
	return rv, err
}

func (r *SecurityGroupRepo) Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	pgUID, err := strToUUID(sg.UID)
	if err != nil {
		return nil, err
	}
	pgNetID, err := strToUUID(sg.NetworkID)
	if err != nil {
		return nil, err
	}
	pgFolderID, err := strToUUID(sg.FolderID)
	if err != nil {
		return nil, err
	}
	pgCloudID, _ := strToUUID(sg.CloudID)
	pgOrgID, _ := strToUUID(sg.OrganizationID)

	var result *domain.SecurityGroup
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`INSERT INTO security_groups (uid, network_id, folder_id, cloud_id, organization_id, name, labels, annotations, spec, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 RETURNING `+sgColumns,
			pgUID, pgNetID, pgFolderID, pgCloudID, pgOrgID, sg.Name,
			mapToJSONB(sg.Labels), mapToJSONB(sg.Annotations),
			domainSGToSpec(sg), domainSGToStatus(sg),
		)
		res, scanErr := scanSG(row)
		if scanErr != nil {
			return scanErr
		}
		result = res

		data, _ := proto.Marshal(domainSGToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "ADDED",
			ResourceKind: "SecurityGroup",
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

func (r *SecurityGroupRepo) Update(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	pgUID, err := strToUUID(sg.UID)
	if err != nil {
		return nil, err
	}

	var result *domain.SecurityGroup
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`UPDATE security_groups SET labels = $2, annotations = $3, spec = $4, generation = generation + 1
			 WHERE uid = $1 RETURNING `+sgColumns,
			pgUID, mapToJSONB(sg.Labels), mapToJSONB(sg.Annotations), domainSGToSpec(sg),
		)
		res, scanErr := scanSG(row)
		if scanErr != nil {
			return scanErr
		}
		result = res

		data, _ := proto.Marshal(domainSGToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "MODIFIED",
			ResourceKind: "SecurityGroup",
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

func (r *SecurityGroupRepo) HardDelete(ctx context.Context, uid string) error {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return err
	}
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		_, delErr := tx.Exec(ctx, `DELETE FROM security_groups WHERE uid = $1`, pgUID)
		if delErr != nil {
			return delErr
		}
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "DELETED",
			ResourceKind: "SecurityGroup",
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

func (r *SecurityGroupRepo) HasDependents(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func scanSG(row pgx.Row) (*domain.SecurityGroup, error) {
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

	var specData sgSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData sgStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	return &domain.SecurityGroup{
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
		Rules:             specToSGRules(spec),
		State:             statusData.State,
	}, nil
}

func scanSGFromRows(rows pgx.Rows) (*domain.SecurityGroup, error) {
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

	var specData sgSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData sgStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	return &domain.SecurityGroup{
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
		Rules:             specToSGRules(spec),
		State:             statusData.State,
	}, nil
}

func buildListQuerySG(br selector.BuildResult, pageClause string) string {
	var sb strings.Builder
	sb.WriteString(`SELECT ` + sgColumns + ` FROM security_groups WHERE deletion_timestamp IS NULL`)
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

func domainSGToProto(sg *domain.SecurityGroup) *pb.SecurityGroup {
	meta := &commonv1.ResourceMeta{
		Uid:             sg.UID,
		Name:            sg.Name,
		FolderId:        sg.FolderID,
		CloudId:         sg.CloudID,
		OrganizationId:  sg.OrganizationID,
		Labels:          sg.Labels,
		Annotations:     sg.Annotations,
		ResourceVersion: fmt.Sprintf("%d", sg.ResourceVersion),
		Generation:      sg.Generation,
		Finalizers:      sg.Finalizers,
	}
	if !sg.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(sg.CreationTimestamp)
	}
	if sg.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*sg.DeletionTimestamp)
	}

	rules := make([]*pb.SecurityGroupRule, len(sg.Rules))
	for i, r := range sg.Rules {
		rules[i] = &pb.SecurityGroupRule{
			Id:           r.ID,
			Direction:    r.Direction,
			Protocol:     r.Protocol,
			PortRangeMin: r.PortRangeMin,
			PortRangeMax: r.PortRangeMax,
			CidrBlocks:   r.CIDRBlocks,
			Description:  r.Description,
		}
	}

	return &pb.SecurityGroup{
		Metadata: meta,
		Spec: &pb.SecurityGroupSpec{
			NetworkId:   sg.NetworkID,
			DisplayName: sg.DisplayName,
			Description: sg.Description,
			Rules:       rules,
		},
		Status: &pb.SecurityGroupStatus{State: sg.State},
	}
}
