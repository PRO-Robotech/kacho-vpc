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

const subnetColumns = `uid, network_id, folder_id, cloud_id, organization_id, name, labels, annotations,
	creation_timestamp, resource_version, generation, deletion_timestamp, finalizers, spec, status`

// SubnetRepo реализует service.SubnetRepo.
type SubnetRepo struct {
	pool         *pgxpool.Pool
	transactor   *coredb.Transactor
	outboxWriter *outbox.Writer
}

// NewSubnetRepo создаёт SubnetRepo.
func NewSubnetRepo(pool *pgxpool.Pool, transactor *coredb.Transactor, outboxWriter *outbox.Writer) *SubnetRepo {
	return &SubnetRepo{pool: pool, transactor: transactor, outboxWriter: outboxWriter}
}

func (r *SubnetRepo) GetByUID(ctx context.Context, uid string) (*domain.Subnet, error) {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("uid", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+subnetColumns+` FROM subnets WHERE uid = $1 AND deletion_timestamp IS NULL`, pgUID)
	return scanSubnet(row)
}

func (r *SubnetRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Subnet, error) {
	pgFolderID, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid format").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+subnetColumns+` FROM subnets WHERE folder_id = $1 AND name = $2 AND deletion_timestamp IS NULL`,
		pgFolderID, name)
	return scanSubnet(row)
}

func (r *SubnetRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.Subnet, string, int64, error) {
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

	// C6: поддержка фильтра по network_id через refs
	var networkIDClause string
	var networkIDArgs []any
	for _, s := range selectors {
		if s.NetworkID != "" {
			pgNetID, parseErr := strToUUID(s.NetworkID)
			if parseErr == nil {
				networkIDArgs = append(networkIDArgs, pgNetID)
				networkIDClause = fmt.Sprintf("network_id = $%d", paramBase+len(networkIDArgs)-1)
				paramBase++
			}
		}
	}

	query := buildListQuerySubnet(br, pageClause, networkIDClause)
	args := append(br.Args, pageArgs...)
	args = append(args, networkIDArgs...)
	args = append(args, pageSize+1)
	limitParam := fmt.Sprintf("$%d", paramBase)
	query += fmt.Sprintf(" ORDER BY resource_version ASC, uid ASC LIMIT %s", limitParam)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()

	var subnets []*domain.Subnet
	for rows.Next() {
		s, scanErr := scanSubnetFromRows(rows)
		if scanErr != nil {
			return nil, "", 0, scanErr
		}
		subnets = append(subnets, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, err
	}

	var nextToken string
	if len(subnets) > pageSize {
		subnets = subnets[:pageSize]
		last := subnets[len(subnets)-1]
		tok := selector.PageToken{LastResourceVersion: last.ResourceVersion, LastUID: last.UID}
		b, _ := json.Marshal(tok)
		nextToken = string(b)
	}

	return subnets, nextToken, snapshotRV, nil
}

func (r *SubnetRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	var rv int64
	err := r.pool.QueryRow(ctx, `SELECT last_value FROM resource_version_seq`).Scan(&rv)
	return rv, err
}

func (r *SubnetRepo) Insert(ctx context.Context, subnet *domain.Subnet) (*domain.Subnet, error) {
	pgUID, err := strToUUID(subnet.UID)
	if err != nil {
		return nil, err
	}
	pgNetID, err := strToUUID(subnet.NetworkID)
	if err != nil {
		return nil, err
	}
	pgFolderID, err := strToUUID(subnet.FolderID)
	if err != nil {
		return nil, err
	}
	pgCloudID, _ := strToUUID(subnet.CloudID)
	pgOrgID, _ := strToUUID(subnet.OrganizationID)

	var result *domain.Subnet
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`INSERT INTO subnets (uid, network_id, folder_id, cloud_id, organization_id, name, labels, annotations, spec, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 RETURNING `+subnetColumns,
			pgUID, pgNetID, pgFolderID, pgCloudID, pgOrgID, subnet.Name,
			mapToJSONB(subnet.Labels), mapToJSONB(subnet.Annotations),
			domainSubnetToSpec(subnet), domainSubnetToStatus(subnet),
		)
		s, scanErr := scanSubnet(row)
		if scanErr != nil {
			return scanErr
		}
		result = s

		data, _ := proto.Marshal(domainSubnetToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "ADDED",
			ResourceKind: "Subnet",
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

func (r *SubnetRepo) Update(ctx context.Context, subnet *domain.Subnet) (*domain.Subnet, error) {
	pgUID, err := strToUUID(subnet.UID)
	if err != nil {
		return nil, err
	}

	var result *domain.Subnet
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`UPDATE subnets SET labels = $2, annotations = $3, spec = $4, generation = generation + 1
			 WHERE uid = $1 RETURNING `+subnetColumns,
			pgUID, mapToJSONB(subnet.Labels), mapToJSONB(subnet.Annotations), domainSubnetToSpec(subnet),
		)
		s, scanErr := scanSubnet(row)
		if scanErr != nil {
			return scanErr
		}
		result = s

		data, _ := proto.Marshal(domainSubnetToProto(result))
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "MODIFIED",
			ResourceKind: "Subnet",
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

func (r *SubnetRepo) HardDelete(ctx context.Context, uid string) error {
	pgUID, err := strToUUID(uid)
	if err != nil {
		return err
	}
	txErr := r.transactor.InTx(ctx, func(tx pgx.Tx) error {
		_, delErr := tx.Exec(ctx, `DELETE FROM subnets WHERE uid = $1`, pgUID)
		if delErr != nil {
			return delErr
		}
		_, evtErr := r.outboxWriter.WriteEvent(ctx, tx, outbox.Event{
			EventType:    "DELETED",
			ResourceKind: "Subnet",
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

func scanSubnet(row pgx.Row) (*domain.Subnet, error) {
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

	var specData subnetSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData subnetStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	return &domain.Subnet{
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
		CIDRBlock:         specData.CIDRBlock,
		ZoneID:            specData.ZoneID,
		DisplayName:       specData.DisplayName,
		Description:       specData.Description,
		State:             statusData.State,
	}, nil
}

func scanSubnetFromRows(rows pgx.Rows) (*domain.Subnet, error) {
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

	var specData subnetSpecJSON
	_ = json.Unmarshal(spec, &specData)
	var statusData subnetStatusJSON
	_ = json.Unmarshal(statusBytes, &statusData)

	return &domain.Subnet{
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
		CIDRBlock:         specData.CIDRBlock,
		ZoneID:            specData.ZoneID,
		DisplayName:       specData.DisplayName,
		Description:       specData.Description,
		State:             statusData.State,
	}, nil
}

func buildListQuerySubnet(br selector.BuildResult, pageClause, networkIDClause string) string {
	var sb strings.Builder
	sb.WriteString(`SELECT ` + subnetColumns + ` FROM subnets WHERE deletion_timestamp IS NULL`)

	if br.WhereClause != "" {
		clause := strings.TrimPrefix(br.WhereClause, "WHERE ")
		sb.WriteString(" AND (")
		sb.WriteString(clause)
		sb.WriteString(")")
	}

	if networkIDClause != "" {
		sb.WriteString(" AND ")
		sb.WriteString(networkIDClause)
	}

	if pageClause != "" {
		sb.WriteString(" AND (")
		sb.WriteString(pageClause)
		sb.WriteString(")")
	}

	return sb.String()
}

func domainSubnetToProto(s *domain.Subnet) *pb.Subnet {
	meta := &commonv1.ResourceMeta{
		Uid:             s.UID,
		Name:            s.Name,
		FolderId:        s.FolderID,
		CloudId:         s.CloudID,
		OrganizationId:  s.OrganizationID,
		Labels:          s.Labels,
		Annotations:     s.Annotations,
		ResourceVersion: fmt.Sprintf("%d", s.ResourceVersion),
		Generation:      s.Generation,
		Finalizers:      s.Finalizers,
	}
	if !s.CreationTimestamp.IsZero() {
		meta.CreationTimestamp = timestamppb.New(s.CreationTimestamp)
	}
	if s.DeletionTimestamp != nil {
		meta.DeletionTimestamp = timestamppb.New(*s.DeletionTimestamp)
	}
	return &pb.Subnet{
		Metadata: meta,
		Spec: &pb.SubnetSpec{
			NetworkId:   s.NetworkID,
			CidrBlock:   s.CIDRBlock,
			ZoneId:      s.ZoneID,
			DisplayName: s.DisplayName,
			Description: s.Description,
		},
		Status: &pb.SubnetStatus{State: s.State},
	}
}
