package service

import (
	"net/netip"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
