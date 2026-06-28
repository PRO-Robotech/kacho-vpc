// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package pbconv — общие proto-конвертеры, переиспользуемые use-case-хендлерами
// kacho-vpc (маппинг operation→proto, извлечение FGA-subject).
package pbconv

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	operationpb "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OperationToProto — конвертирует corelib operations.Operation в его proto-форму.
func OperationToProto(op *operations.Operation) *operationpb.Operation {
	if op == nil {
		return nil
	}
	p := &operationpb.Operation{
		Id:                   op.ID,
		Description:          op.Description,
		CreatedAt:            timestamppb.New(op.CreatedAt),
		CreatedBy:            op.CreatedBy,
		ModifiedAt:           timestamppb.New(op.ModifiedAt),
		Done:                 op.Done,
		Metadata:             op.Metadata,
		PrincipalType:        op.Principal.Type,
		PrincipalId:          op.Principal.ID,
		PrincipalDisplayName: op.Principal.DisplayName,
	}
	if op.Error != nil {
		p.Result = &operationpb.Operation_Error{Error: op.Error}
	} else if op.Response != nil {
		p.Result = &operationpb.Operation_Response{Response: op.Response}
	}
	return p
}

// SubjectFromContext — извлекает FGA-subject ("user:usr_x"/"service_account:sva_x")
// из Principal запроса. Различает ТРИ случая (раньше все три коллапсировали в "",
// из-за чего List-фильтр трактовал «identity не извлечён» как «доверенный system»
// и отдавал нефильтрованный список — leak):
//   - реальный user/service_account → "<type>:<id>" (идёт в ListObjects-фильтр);
//   - доверенный system-principal → authzfilter.SystemSubject (явный passthrough);
//   - identity не извлечён (anon / gateway не проставил principal) → "" — это
//     «не знаю, кто ты», List-use-case обязан fail-closed (пустой список), НЕ passthrough.
func SubjectFromContext(ctx context.Context) string {
	// PrincipalFromContextOK различает АНОНИМНЫЙ ctx (principal не устанавливался —
	// ok=false) от ЯВНО установленного principal. Без этого различения anonymous и
	// system-principal оба коллапсировали в SystemPrincipal → List-фильтр трактовал
	// anon как доверенный system и отдавал нефильтрованный список (leak).
	p, ok := operations.PrincipalFromContextOK(ctx)
	if !ok {
		// principal не извлечён (anon / gateway не проставил identity) → fail-closed.
		return ""
	}
	if p.Type == "system" {
		// явный доверенный system-вызов → passthrough-sentinel.
		return authzfilter.SystemSubject
	}
	if p.Type == "" || p.ID == "" {
		return ""
	}
	return p.Type + ":" + p.ID
}
