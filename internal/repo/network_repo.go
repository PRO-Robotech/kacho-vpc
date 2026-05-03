package repo

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

const networkCols = `id, folder_id, name, description, created_at, labels,
	status, generation, resource_version, observed_generation, status_last_transition_at, deleted_at`

// NetworkRepo реализует service.NetworkRepo.
type NetworkRepo struct {
	pool *pgxpool.Pool
}

// NewNetworkRepo создаёт NetworkRepo.
func NewNetworkRepo(pool *pgxpool.Pool) *NetworkRepo {
	return &NetworkRepo{pool: pool}
}

func (r *NetworkRepo) Get(ctx context.Context, id string) (*domain.Network, error) {
	uid, err := strToUUID(id)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+networkCols+` FROM networks WHERE id = $1 AND deleted_at IS NULL`, uid)
	return scanNetwork(row)
}

func (r *NetworkRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Network, error) {
	fid, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+networkCols+` FROM networks WHERE folder_id = $1 AND name = $2 AND deleted_at IS NULL`,
		fid, name)
	return scanNetwork(row)
}

func (r *NetworkRepo) List(ctx context.Context, filter service.ListFilter) ([]domain.Network, string, error) {
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
	orderBy := "ORDER BY created_at ASC, id ASC"
	if filter.OrderBy != "" {
		orderBy = buildOrderBy(filter.OrderBy)
	}

	q := fmt.Sprintf(`SELECT `+networkCols+` FROM networks %s %s LIMIT $%d`, where, orderBy, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var result []domain.Network
	for rows.Next() {
		n, serr := scanNetworkRow(rows)
		if serr != nil {
			return nil, "", serr
		}
		result = append(result, *n)
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

func (r *NetworkRepo) Create(ctx context.Context, n *domain.Network) error {
	uid, err := strToUUID(n.ID)
	if err != nil {
		return err
	}
	fid, err := strToUUID(n.FolderID)
	if err != nil {
		return err
	}
	statusStr := domain.NetworkStatusString[n.Status]
	if statusStr == "" {
		statusStr = "NETWORK_STATUS_PROVISIONING"
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO networks (id, folder_id, name, description, labels, status, generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uid, fid, n.Name, n.Description, mapToJSON(n.Labels), statusStr, n.Generation,
	)
	return err
}

func (r *NetworkRepo) Update(ctx context.Context, n *domain.Network) error {
	uid, err := strToUUID(n.ID)
	if err != nil {
		return err
	}
	statusStr := domain.NetworkStatusString[n.Status]
	if statusStr == "" {
		statusStr = "NETWORK_STATUS_ACTIVE"
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE networks
		SET name = $2, description = $3, labels = $4, status = $5, generation = $6,
		    resource_version = gen_random_uuid()::text,
		    status_last_transition_at = now()
		WHERE id = $1 AND deleted_at IS NULL`,
		uid, n.Name, n.Description, mapToJSON(n.Labels), statusStr, n.Generation,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("Network", n.ID).Err()
	}
	return nil
}

func (r *NetworkRepo) SoftDelete(ctx context.Context, id string) error {
	uid, err := strToUUID(id)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE networks SET deleted_at = now(), status = 'NETWORK_STATUS_DELETING' WHERE id = $1 AND deleted_at IS NULL`,
		uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("Network", id).Err()
	}
	return nil
}

func (r *NetworkRepo) HasDependents(ctx context.Context, id string) (bool, error) {
	uid, err := strToUUID(id)
	if err != nil {
		return false, err
	}
	var count int
	err = r.pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM subnets WHERE network_id = $1 AND deleted_at IS NULL) +
		       (SELECT COUNT(*) FROM security_groups WHERE network_id = $1 AND deleted_at IS NULL) +
		       (SELECT COUNT(*) FROM route_tables WHERE network_id = $1 AND deleted_at IS NULL)`,
		uid).Scan(&count)
	return count > 0, err
}

// ---- scan helpers ----

func scanNetwork(row pgx.Row) (*domain.Network, error) {
	n, err := scanNetworkFields(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return n, err
}

func scanNetworkRow(rows pgx.Rows) (*domain.Network, error) {
	return scanNetworkFields(rows.Scan)
}

func scanNetworkFields(scanFn func(...any) error) (*domain.Network, error) {
	var (
		id, folderID, name, description string
		createdAt                        pgtype.Timestamptz
		labelsJSON                       []byte
		statusStr                        string
		generation                       int64
		resourceVersion                  string
		observedGeneration               int64
		statusLastTransition             pgtype.Timestamptz
		deletedAt                        pgtype.Timestamptz
	)
	err := scanFn(
		&id, &folderID, &name, &description, &createdAt, &labelsJSON,
		&statusStr, &generation, &resourceVersion, &observedGeneration,
		&statusLastTransition, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &domain.Network{
		ID:                     id,
		FolderID:               folderID,
		Name:                   name,
		Description:            description,
		CreatedAt:              tsToTime(createdAt),
		Labels:                 jsonToMap(labelsJSON),
		Status:                 domain.ParseNetworkStatus(statusStr),
		Generation:             generation,
		ResourceVersion:        resourceVersion,
		ObservedGeneration:     observedGeneration,
		StatusLastTransitionAt: tsToTime(statusLastTransition),
		DeletedAt:              tsToTimePtr(deletedAt),
	}, nil
}

// ---- pagination helpers ----

type networkPageCursor struct {
	CreatedAt time.Time
	ID        string
}

func encodeNetworkPageToken(t time.Time, id string) string {
	raw := strconv.FormatInt(t.UnixNano(), 10) + ":" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeNetworkPageToken(token string) (networkPageCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return networkPageCursor{}, err
	}
	parts := strings.SplitN(string(b), ":", 2)
	if len(parts) != 2 {
		return networkPageCursor{}, fmt.Errorf("malformed token")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return networkPageCursor{}, err
	}
	return networkPageCursor{CreatedAt: time.Unix(0, ns).UTC(), ID: parts[1]}, nil
}

// buildOrderBy строит ORDER BY из строки "field asc/desc".
func buildOrderBy(orderBy string) string {
	parts := strings.Fields(orderBy)
	if len(parts) == 0 {
		return "ORDER BY created_at ASC, id ASC"
	}
	col := strings.ToLower(parts[0])
	dir := "ASC"
	if len(parts) > 1 && strings.ToUpper(parts[1]) == "DESC" {
		dir = "DESC"
	}
	allowed := map[string]string{
		"created_at": "created_at",
		"name":       "name",
	}
	if dbCol, ok := allowed[col]; ok {
		return fmt.Sprintf("ORDER BY %s %s, id ASC", dbCol, dir)
	}
	return "ORDER BY created_at ASC, id ASC"
}

