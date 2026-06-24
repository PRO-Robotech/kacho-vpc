// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"context"

	"google.golang.org/grpc"

	"github.com/PRO-Robotech/kacho-corelib/auth"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
)

// NewIAMAuthorizeClient оборачивает gRPC conn в authzfilter.AuthorizeClient.
// conn указывает на listener kacho-iam AuthorizeService (публичная проекция authz-слоя).
// Используется per-object List-фильтром.
func NewIAMAuthorizeClient(conn grpc.ClientConnInterface) authzfilter.AuthorizeClient {
	return &grpcAuthorizeClient{cli: iamv1.NewAuthorizeServiceClient(conn)}
}

type grpcAuthorizeClient struct {
	cli iamv1.AuthorizeServiceClient
}

// ListObjects пробрасывает request в kacho-iam AuthorizeService.
//
// outgoing ctx обернут `auth.PropagateOutgoing`, чтобы principal-extract на стороне
// iam увидел реального caller'а, а не SystemPrincipal. Без wrap'а IAM-authzguard'ы
// видят "system:bootstrap" и отбивают ListObjects как anonymous-mutation → vpc
// list-filter вернул бы 403/Unavailable для всех subject'ов.
func (g *grpcAuthorizeClient) ListObjects(ctx context.Context, req *iamv1.ListObjectsRequest, opts ...grpc.CallOption) (*iamv1.ListObjectsResponse, error) {
	return g.cli.ListObjects(auth.PropagateOutgoing(ctx), req, opts...)
}
