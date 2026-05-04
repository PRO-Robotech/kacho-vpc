package service

import (
	"net/netip"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
)

// validateID проверяет что value — синтаксически валидный resource id:
// либо в формате "<3-char prefix><17-char crockford-base32>" (см.
// ids.HasKnownPrefix), либо в legacy-UUID-формате (на случай миграционного
// окна или внешних клиентов, передающих старые id).
//
// Возвращает gRPC InvalidArgument с BadRequest.field_violations[] если ни
// один формат не подошёл. Используется в Create/Update сервисах перед
// запуском Operation worker'а: garbage-id (PG SQLSTATE 22P02) и не-id
// формат должен быть синхронным `code:3 INVALID_ARGUMENT`, не async
// `code:5 NOT_FOUND` (см. финдинги F-CR-INVALID-UUID-mapping,
// N-CR-INVALID-UUID-mapping, A-CR-INVALID-FOLDER).
//
//nolint:unused // оставлен как util для будущего pre-validation; вызовы — в TODO
func validateID(field, value string) error {
	if ids.HasKnownPrefix(value) {
		return nil
	}
	if _, err := uuid.Parse(value); err == nil {
		return nil
	}
	st := status.New(codes.InvalidArgument, field+" must be a valid resource id")
	br := &errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{
			{Field: field, Description: field + " must be a valid resource id"},
		},
	}
	if withDetails, derr := st.WithDetails(br); derr == nil {
		return withDetails.Err()
	}
	return st.Err()
}

// validateCIDRPrefix проверяет, что value — валидный CIDR-prefix (например
// "10.0.0.0/24") и host-bits = 0 (т.е. value совпадает с .Masked()).
//
// YC verbatim: `10.0.0.5/24` → INVALID_ARGUMENT (host-bits not zero). Если в
// конкретном поле допустимы и IPv4, и IPv6 — оба варианта проходят через ту же
// проверку. См. SU-CIDR-2-host-bits.md.
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
