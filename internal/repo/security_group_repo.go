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

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

const sgCols = `id, folder_id, network_id, name, description,
	created_at, labels, status, generation, resource_version, rules, deleted_at`

// SecurityGroupRepo реализует service.SecurityGroupRepo.
type SecurityGroupRepo struct {
	pool *pgxpool.Pool
}

// NewSecurityGroupRepo создаёт SecurityGroupRepo.
func NewSecurityGroupRepo(pool *pgxpool.Pool) *SecurityGroupRepo {
	return &SecurityGroupRepo{pool: pool}
}

func (r *SecurityGroupRepo) Get(ctx context.Context, id string) (*domain.SecurityGroup, error) {
	uid, err := strToUUID(id)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+sgCols+` FROM security_groups WHERE id = $1 AND deleted_at IS NULL`, uid)
	return scanSG(row)
}

func (r *SecurityGroupRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.SecurityGroup, error) {
	fid, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+sgCols+` FROM security_groups WHERE folder_id = $1 AND name = $2 AND deleted_at IS NULL`,
		fid, name)
	return scanSG(row)
}

func (r *SecurityGroupRepo) List(ctx context.Context, filter service.ListFilter) ([]domain.SecurityGroup, string, error) {
	pageSize := filter.PageSize
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}

	args := []any{}
	conds := []string{"deleted_at IS NULL"}
	argIdx := 1

	if filter.FolderID != "" {
		fid, err := strToUUID(filter.FolderID)
		if err != nil {
			return nil, "", coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid").Err()
		}
		conds = append(conds, fmt.Sprintf("folder_id = $%d", argIdx))
		args = append(args, fid)
		argIdx++
	}

	if filter.PageToken != "" {
		cur, decErr := decodeNetworkPageToken(filter.PageToken)
		if decErr == nil {
			conds = append(conds, fmt.Sprintf("(created_at, id::text) > ($%d, $%d)", argIdx, argIdx+1))
			args = append(args, cur.CreatedAt, cur.ID)
			argIdx += 2
		}
	}

	where := "WHERE " + strings.Join(conds, " AND ")
	orderBy := buildOrderBy(filter.OrderBy)

	q := fmt.Sprintf(`SELECT `+sgCols+` FROM security_groups %s %s LIMIT $%d`, where, orderBy, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var result []domain.SecurityGroup
	for rows.Next() {
		sg, serr := scanSGRow(rows)
		if serr != nil {
			return nil, "", serr
		}
		result = append(result, *sg)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodeNetworkPageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

func (r *SecurityGroupRepo) Create(ctx context.Context, sg *domain.SecurityGroup) error {
	uid, err := strToUUID(sg.ID)
	if err != nil {
		return err
	}
	fid, err := strToUUID(sg.FolderID)
	if err != nil {
		return err
	}
	nid, err := strToUUID(sg.NetworkID)
	if err != nil {
		return err
	}
	statusStr := domain.SecurityGroupStatusString[sg.Status]
	if statusStr == "" {
		statusStr = "SECURITY_GROUP_STATUS_PROVISIONING"
	}
	rulesJSON, merr := marshalRules(sg.Rules)
	if merr != nil {
		return merr
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO security_groups (id, folder_id, network_id, name, description, labels, status, generation, rules)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		uid, fid, nid, sg.Name, sg.Description, mapToJSON(sg.Labels), statusStr, sg.Generation, rulesJSON,
	)
	return err
}

func (r *SecurityGroupRepo) Update(ctx context.Context, sg *domain.SecurityGroup) error {
	uid, err := strToUUID(sg.ID)
	if err != nil {
		return err
	}
	statusStr := domain.SecurityGroupStatusString[sg.Status]
	if statusStr == "" {
		statusStr = "SECURITY_GROUP_STATUS_ACTIVE"
	}
	rulesJSON, merr := marshalRules(sg.Rules)
	if merr != nil {
		return merr
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE security_groups
		SET name = $2, description = $3, labels = $4, status = $5, generation = $6,
		    rules = $7, resource_version = gen_random_uuid()::text
		WHERE id = $1 AND deleted_at IS NULL`,
		uid, sg.Name, sg.Description, mapToJSON(sg.Labels), statusStr, sg.Generation, rulesJSON,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("SecurityGroup", sg.ID).Err()
	}
	return nil
}

func (r *SecurityGroupRepo) SoftDelete(ctx context.Context, id string) error {
	uid, err := strToUUID(id)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE security_groups SET deleted_at = now(), status = 'SECURITY_GROUP_STATUS_DELETING' WHERE id = $1 AND deleted_at IS NULL`,
		uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("SecurityGroup", id).Err()
	}
	return nil
}

// ---- scan helpers ----

func scanSG(row pgx.Row) (*domain.SecurityGroup, error) {
	sg, err := scanSGFields(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return sg, err
}

func scanSGRow(rows pgx.Rows) (*domain.SecurityGroup, error) {
	return scanSGFields(rows.Scan)
}

func scanSGFields(scanFn func(...any) error) (*domain.SecurityGroup, error) {
	var (
		id, folderID, networkID, name, description string
		createdAt                                   pgtype.Timestamptz
		labelsJSON, rulesJSON                       []byte
		statusStr                                   string
		generation                                  int64
		resourceVersion                             string
		deletedAt                                   pgtype.Timestamptz
	)
	err := scanFn(
		&id, &folderID, &networkID, &name, &description,
		&createdAt, &labelsJSON, &statusStr, &generation, &resourceVersion, &rulesJSON, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	rules, _ := unmarshalRules(rulesJSON)
	return &domain.SecurityGroup{
		ID:              id,
		FolderID:        folderID,
		NetworkID:       networkID,
		Name:            name,
		Description:     description,
		CreatedAt:       tsToTime(createdAt),
		Labels:          jsonToMap(labelsJSON),
		Status:          domain.ParseSecurityGroupStatus(statusStr),
		Generation:      generation,
		ResourceVersion: resourceVersion,
		Rules:           rules,
		DeletedAt:       tsToTimePtr(deletedAt),
	}, nil
}

// ---- JSON helpers for rules/routes ----

type ruleJSON struct {
	ID           string   `json:"id"`
	Direction    string   `json:"direction"`
	Protocol     string   `json:"protocol"`
	PortRangeMin int32    `json:"port_range_min"`
	PortRangeMax int32    `json:"port_range_max"`
	CIDRBlocks   []string `json:"cidr_blocks"`
	Description  string   `json:"description"`
}

func marshalRules(rules []domain.SecurityGroupRule) ([]byte, error) {
	if len(rules) == 0 {
		return []byte("[]"), nil
	}
	items := make([]ruleJSON, len(rules))
	for i, r := range rules {
		items[i] = ruleJSON{
			ID:           r.ID,
			Direction:    r.Direction,
			Protocol:     r.Protocol,
			PortRangeMin: r.PortRangeMin,
			PortRangeMax: r.PortRangeMax,
			CIDRBlocks:   r.CIDRBlocks,
			Description:  r.Description,
		}
	}
	return json.Marshal(items)
}

func unmarshalRules(b []byte) ([]domain.SecurityGroupRule, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var items []ruleJSON
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	rules := make([]domain.SecurityGroupRule, len(items))
	for i, r := range items {
		rules[i] = domain.SecurityGroupRule{
			ID:           r.ID,
			Direction:    r.Direction,
			Protocol:     r.Protocol,
			PortRangeMin: r.PortRangeMin,
			PortRangeMax: r.PortRangeMax,
			CIDRBlocks:   r.CIDRBlocks,
			Description:  r.Description,
		}
	}
	return rules, nil
}
