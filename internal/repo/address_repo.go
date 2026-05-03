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

const addrColumns = `uid, folder_id, cloud_id, organization_id, name, labels, annotations,
	creation_timestamp, resource_version, generation, deletion_timestamp, finalizers, spec, status, allocated_ipv4`

// AddressRepo реализует service.AddressRepo.
type AddressRepo struct {
	pool         *pgxpool.Pool
	transactor   *coredb.Transactor
	outboxWriter *outbox.Writer
}

// NewAddressRepo создаёт AddressRepo.
func NewAddressRepo(pool *pgxpool.Pool, transactor *coredb.Transactor, outboxWriter *outbox.Writer) *AddressRepo {
	return &AddressRepo{pool: pool, transactor: transactor, outboxWriter: outboxWriter}
}

func (r *AddressRepo) GetByUID(ctx context.Context, uid string) (*domain.Address, error) {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+addrColumns+` FROM addresses WHERE uid = $1 AND deletion_timestamp IS NULL`, pgUID)
	return scanAddress(row)
}

func (r *AddressRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Address, error) {
	pgFolderID, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+addrColumns+` FROM addresses WHERE folder_id = $1 AND name = $2 AND deletion_timestamp IS NULL`,
		pgFolderID, name)
	return scanAddress(row)
}

func (r *AddressRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.Address, string, int64, error) {
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

	query := buildListQueryAddr(br, pageClause)
	args := append(br.Args, pageArgs...)
	args = append(args, pageSize+1)
	limitParam := fmt.Sprintf("$%d", paramBase)
	query += fmt.Sprintf(" ORDER BY resource_version ASC, uid ASC LIMIT %s", limitParam)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()

	var addrs []*domain.Address
	for rows.Next() {
		a, scanErr := scanAddressFromRows(rows)
		if scanErr != nil {
			return nil, "", 0, scanErr
		}
		addrs = append(addrs, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, err
	}

	var nextToken string
	if len(addrs) > pageSize {
		addrs = addrs[:pageSize]
		last := addrs[len(addrs)-1]
		tok := selector.PageToken{LastResourceVersion: last.ResourceVersion, LastUID: last.UID}
		b, _ := json.Marshal(tok)
		nextToken = string(b)
	}
	return addrs, nextToken, snapshotRV, nil
}

func (r *AddressRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	var rv int64
	err := r.pool.QueryRow(ctx, `SELECT last_value FROM resource_version_seq`).Scan(&rv)
	return rv, err
}

func (r *AddressRepo) Insert(ctx context.Context, addr *domain.Address) (*domain.Address, error) {
	pgUID, err := strToUUID(addr.UID)
	if err != nil {
		return nil, err
	}
	pgFolderID, err := strToUUID(addr.FolderID)
	if err != nil {
		return nil, err
	}
	pgCloudID, _ := strToUUID(addr.CloudID)
	pgOrgID, _ := strToUUID(addr.OrganizationID)

	var result *domain.Address
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		var allocIPArg interface{} = addr.AllocatedIPv4
		if addr.AllocatedIPv4 == "" {
			allocIPArg = nil
		}
		row := tx.QueryRow(ctx,
			`INSERT INTO addresses (uid, folder_id, cloud_id, organization_id, name, labels, annotations, spec, status, allocated_ipv4)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 RETURNING `+addrColumns,
			pgUID, pgFolderID, pgCloudID, pgOrgID, addr.Name,
			mapToJSONB(addr.Labels), mapToJSONB(addr.Annotations),
			domainAddrToSpec(addr), domainAddrToStatus(addr), allocIPArg,
		)
		a, scanErr := scanAddress(row)
		if scanErr != nil {
			return scanErr
		}
		result = a

		data, _ := proto.Marshal(domainAddrToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "ADDED",
			ResourceKind: "Address",
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

func (r *AddressRepo) Update(ctx context.Context, addr *domain.Address) (*domain.Address, error) {
	pgUID, err := strToUUID(addr.UID)
	if err != nil {
		return nil, err
	}

	var result *domain.Address
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`UPDATE addresses SET labels = $2, annotations = $3, spec = $4, generation = generation + 1
			 WHERE uid = $1 RETURNING `+addrColumns,
			pgUID, mapToJSONB(addr.Labels), mapToJSONB(addr.Annotations), domainAddrToSpec(addr),
		)
		a, scanErr := scanAddress(row)
		if scanErr != nil {
			return scanErr
		}
		result = a

		data, _ := proto.Marshal(domainAddrToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "MODIFIED",
			ResourceKind: "Address",
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

func (r *AddressRepo) UpdateStatus(ctx context.Context, uid, state string) error {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return err
	}
	statusJSON, _ := json.Marshal(addrStatusJSON{State: state})
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		_, updateErr := tx.Exec(ctx,
			`UPDATE addresses SET status = $2, generation = generation + 1 WHERE uid = $1`,
			pgUID, statusJSON,
		)
		if updateErr != nil {
			return updateErr
		}
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "MODIFIED",
			ResourceKind: "Address",
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

func (r *AddressRepo) HardDelete(ctx context.Context, uid string) error {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return err
	}
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		_, delErr := tx.Exec(ctx, `DELETE FROM addresses WHERE uid = $1`, pgUID)
		if delErr != nil {
			return delErr
		}
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "DELETED",
			ResourceKind: "Address",
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

func (r *AddressRepo) HasDependents(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func scanAddress(row pgx.Row) (*domain.Address, error) {
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
		allocatedIPv4     *string
	)
	err := row.Scan(&uid, &folderID, &cloudID, &orgID, &name, &labels, &annotations,
		&creationTimestamp, &resourceVersion, &generation, &deletionTimestamp, &finalizers,
		&spec, &statusBytes, &allocatedIPv4)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	var specData addrSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData addrStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	allocIP := ""
	if allocatedIPv4 != nil {
		allocIP = *allocatedIPv4
	}

	return &domain.Address{
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
		AddressType:       specData.AddressType,
		ZoneID:            specData.ZoneID,
		DisplayName:       specData.DisplayName,
		Description:       specData.Description,
		State:             statusData.State,
		AllocatedIPv4:     allocIP,
	}, nil
}

func scanAddressFromRows(rows pgx.Rows) (*domain.Address, error) {
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
		allocatedIPv4     *string
	)
	err := rows.Scan(&uid, &folderID, &cloudID, &orgID, &name, &labels, &annotations,
		&creationTimestamp, &resourceVersion, &generation, &deletionTimestamp, &finalizers,
		&spec, &statusBytes, &allocatedIPv4)
	if err != nil {
		return nil, err
	}

	var specData addrSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData addrStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	allocIP := ""
	if allocatedIPv4 != nil {
		allocIP = *allocatedIPv4
	}

	return &domain.Address{
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
		AddressType:       specData.AddressType,
		ZoneID:            specData.ZoneID,
		DisplayName:       specData.DisplayName,
		Description:       specData.Description,
		State:             statusData.State,
		AllocatedIPv4:     allocIP,
	}, nil
}

func buildListQueryAddr(br selector.BuildResult, pageClause string) string {
	var sb strings.Builder
	sb.WriteString(`SELECT ` + addrColumns + ` FROM addresses WHERE deletion_timestamp IS NULL`)
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

func domainAddrToProto(a *domain.Address) *pb.Address {
	meta := &commonv1.ResourceMeta{
		Uid:             a.UID,
		Name:            a.Name,
		FolderId:        a.FolderID,
		CloudId:         a.CloudID,
		OrganizationId:  a.OrganizationID,
		Labels:          a.Labels,
		Annotations:     a.Annotations,
		ResourceVersion: fmt.Sprintf("%d", a.ResourceVersion),
		Generation:      a.Generation,
		Finalizers:      a.Finalizers,
	}
	if !a.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(a.CreationTimestamp)
	}
	if a.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*a.DeletionTimestamp)
	}
	return &pb.Address{
		Metadata: meta,
		Spec: &pb.AddressSpec{
			AddressType: a.AddressType,
			ZoneId:      a.ZoneID,
			DisplayName: a.DisplayName,
			Description: a.Description,
		},
		Status: &pb.AddressStatus{
			State:         a.State,
			AllocatedIpv4: a.AllocatedIPv4,
		},
	}
}
