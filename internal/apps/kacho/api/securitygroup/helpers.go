package securitygroup

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует трансферы SecurityGroup/time через init() (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// mapRepoErr — переводит repo-sentinel в gRPC status. Логика идентична
// `service.mapRepoErr`; live-копия здесь нужна, потому что та функция лежит в
// другом пакете и непублична. Wave 3b после переноса всех use-case'ов из
// `internal/service` извлечём общий maperr в shared-leaf.
//
// Sentinel-prefix (`failed precondition: `, `not found`, ...) удаляется при
// преобразовании в gRPC-сообщение, чтобы клиент видел verbatim YC text без
// internal-обёртки.
func mapRepoErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Error(codes.NotFound, stripSentinel(err, repo.ErrNotFound))
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, stripSentinel(err, repo.ErrAlreadyExists))
	case errors.Is(err, repo.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, stripSentinel(err, repo.ErrFailedPrecondition))
	case errors.Is(err, repo.ErrInvalidArg):
		return status.Error(codes.InvalidArgument, stripSentinel(err, repo.ErrInvalidArg))
	case errors.Is(err, repo.ErrInternal):
		return status.Error(codes.Internal, "internal database error")
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		return err
	}
	return status.Error(codes.Internal, "internal database error")
}

// stripSentinel — извлекает «полезную» часть сообщения (после «sentinel: »).
func stripSentinel(err, sentinel error) string {
	msg := err.Error()
	prefix := sentinel.Error() + ": "
	if rest, ok := strings.CutPrefix(msg, prefix); ok {
		return rest
	}
	return msg
}

// checkFolderExists — verbatim YC sync precondition: destination folder must
// exist. Параллельный к `service.checkFolderExists`. См. kacho-vpc#8.
func checkFolderExists(ctx context.Context, fc FolderClient, folderID string) error {
	exists, err := fc.Exists(ctx, folderID)
	if err != nil {
		return status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return status.Errorf(codes.NotFound, "Folder with id %s not found", folderID)
	}
	return nil
}

// checkMoveDestination — sync precondition для Move: dest должен отличаться от
// source и существовать. См. kacho-vpc#10.
func checkMoveDestination(ctx context.Context, fc FolderClient, currentFolderID, destFolderID string) error {
	if destFolderID == currentFolderID {
		return status.Error(codes.InvalidArgument, "Illegal argument Destination folder is the same as the source")
	}
	return checkFolderExists(ctx, fc, destFolderID)
}

// invalidArg — InvalidArgument с BadRequest-details (verbatim YC parity).
func invalidArg(field, desc string) error {
	st := status.New(codes.InvalidArgument, desc)
	br := &errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{
			{Field: field, Description: desc},
		},
	}
	if withDetails, derr := st.WithDetails(br); derr == nil {
		return withDetails.Err()
	}
	return st.Err()
}

// validateCIDRPrefix — host-bits = 0; используется в правилах SG (sync-валидация
// каждого V4/V6 CIDR-блока). Параллельный к `service.validateCIDRPrefix`.
func validateCIDRPrefix(field, value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return invalidArg(field, field+" must be a valid CIDR (e.g. 10.0.0.0/24)")
	}
	if prefix.Masked() != prefix {
		return invalidArg(field,
			field+" must have zero host-bits (use the network address, e.g. 10.0.0.0/24, not 10.0.0.5/24)")
	}
	return nil
}

// validateSGRule — sync-валидация правила.
//
// Direction-семантика и CIDR host-bits — cross-field invariants, не newtype-level
// (description/labels валидируются через r.Validate() внутри SecurityGroup.Validate()).
func validateSGRule(field string, r domain.SecurityGroupRule) error {
	if r.Direction != domain.SecurityGroupRuleDirectionIngress && r.Direction != domain.SecurityGroupRuleDirectionEgress {
		return invalidArg(field+".direction", "direction must be INGRESS or EGRESS")
	}
	if err := r.Description.Validate(); err != nil {
		return err
	}
	if err := domain.ValidateLabels(domain.LabelsFromMap(r.Labels)); err != nil {
		return err
	}
	for i, c := range r.V4CidrBlocks {
		if err := validateCIDRPrefix(fmt.Sprintf("%s.cidr_blocks.v4_cidr_blocks[%d]", field, i), c); err != nil {
			return err
		}
	}
	return nil
}

// assignRuleIDs присваивает каждому rule UID если он пустой.
func assignRuleIDs(rules []domain.SecurityGroupRule) []domain.SecurityGroupRule {
	out := make([]domain.SecurityGroupRule, len(rules))
	for i, r := range rules {
		if r.ID == "" {
			r.ID = ids.NewID(ids.PrefixSecurityGroup)
		}
		out[i] = r
	}
	return out
}

// securityGroupPayloadMap — snapshot SecurityGroup для outbox payload. Wave 5
// replicate (KAC-94, skill evgeniy §6 G.5): use-case Create/Update/Delete/Move
// формирует payload в той же writer-TX, что и DML, через `w.Outbox().Emit(...)`.
// Делегирует exported shim `repo.SecurityGroupPayload`, чтобы держать
// json.Marshal-схему единой со legacy `*repo.SecurityGroupRepo`-консьюмерами.
func securityGroupPayloadMap(sg *kacho.SecurityGroupRecord) map[string]any {
	return repo.SecurityGroupPayload(sg)
}

// marshalSecurityGroupRecord конвертирует repo-entity SG в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4). Используется worker'ами Create/
// Update/UpdateRules/UpdateRule/Move для запихивания результата в Operation.response.
func marshalSecurityGroupRecord(rec *kacho.SecurityGroupRecord) (*anypb.Any, error) {
	var dst *vpcv1.SecurityGroup
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer SecurityGroup: %w", err)
	}
	return anypb.New(dst)
}

// ruleSpecFromProto конвертирует proto SecurityGroupRuleSpec → domain SecurityGroupRule.
//
// Wave 2 batch B (KAC-94): Description — newtype RcDescription, Direction — enum
// SecurityGroupRuleDirection. Labels на rule-уровне остаётся map[string]string
// (см. domain/security_group.go — JSONB-friendly без HDict unexported map).
func ruleSpecFromProto(rs *vpcv1.SecurityGroupRuleSpec) domain.SecurityGroupRule {
	r := domain.SecurityGroupRule{
		Description: domain.RcDescription(rs.Description),
		Labels:      rs.Labels,
	}
	switch rs.Direction {
	case vpcv1.SecurityGroupRule_INGRESS:
		r.Direction = domain.SecurityGroupRuleDirectionIngress
	case vpcv1.SecurityGroupRule_EGRESS:
		r.Direction = domain.SecurityGroupRuleDirectionEgress
	}
	if rs.Ports != nil {
		r.FromPort = rs.Ports.FromPort
		r.ToPort = rs.Ports.ToPort
	}
	if name := rs.GetProtocolName(); name != "" {
		r.ProtocolName = name
	}
	if num := rs.GetProtocolNumber(); num != 0 {
		r.ProtocolNumber = num
	}
	if cb := rs.GetCidrBlocks(); cb != nil {
		r.V4CidrBlocks = cb.V4CidrBlocks
		r.V6CidrBlocks = cb.V6CidrBlocks
	}
	if sgID := rs.GetSecurityGroupId(); sgID != "" {
		r.SecurityGroupID = sgID
	}
	if pred := rs.GetPredefinedTarget(); pred != "" {
		r.PredefinedTarget = pred
	}
	return r
}
