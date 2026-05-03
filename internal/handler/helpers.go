package handler

import (
	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation/v1"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// operationToProto конвертирует domain operation в proto.
func operationToProto(op *operations.Operation) *operationv1.Operation {
	proto := &operationv1.Operation{
		Id:          op.ID,
		Description: op.Description,
		CreatedBy:   op.CreatedBy,
		Done:        op.Done,
		Metadata:    op.Metadata,
	}
	if !op.CreatedAt.IsZero() {
		proto.CreatedAt = timestamppb.New(op.CreatedAt)
	}
	if !op.ModifiedAt.IsZero() {
		proto.ModifiedAt = timestamppb.New(op.ModifiedAt)
	}
	if op.Done {
		if op.Error != nil {
			proto.Result = &operationv1.Operation_Error{
				Error: &status.Status{
					Code:    op.Error.Code,
					Message: op.Error.Message,
				},
			}
		} else if op.Response != nil {
			proto.Result = &operationv1.Operation_Response{
				Response: op.Response,
			}
		}
	}
	return proto
}

// maskFields извлекает пути из FieldMask.
func maskFields(mask interface{ GetPaths() []string }) []string {
	if mask == nil {
		return nil
	}
	return mask.GetPaths()
}
