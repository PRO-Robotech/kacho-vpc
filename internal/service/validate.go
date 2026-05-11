package service

import (
	"context"
	"net/netip"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// checkFolderExists — verbatim YC sync precondition for Create/Move RPCs: the
// destination folder must exist. error → Unavailable "folder check: <err>";
// not-found → NotFound "Folder with id <X> not found". См. kacho-vpc#8.
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
