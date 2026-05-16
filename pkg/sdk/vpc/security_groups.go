package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// SecurityGroupServiceClient — alias на сгенерированный gRPC-клиент.
type SecurityGroupServiceClient = vpcv1.SecurityGroupServiceClient

// GetSecurityGroup — sync read.
func (c *Client) GetSecurityGroup(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.SecurityGroup, error) {
	return c.SecurityGroups.Get(ctx, &vpcv1.GetSecurityGroupRequest{SecurityGroupId: id}, opts...)
}

// ListSecurityGroups — sync list.
func (c *Client) ListSecurityGroups(ctx context.Context, req *vpcv1.ListSecurityGroupsRequest, opts ...grpc.CallOption) (*vpcv1.ListSecurityGroupsResponse, error) {
	return c.SecurityGroups.List(ctx, req, opts...)
}

// CreateSecurityGroup — async create.
func (c *Client) CreateSecurityGroup(ctx context.Context, req *vpcv1.CreateSecurityGroupRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.SecurityGroups.Create(ctx, req, opts...)
}

// UpdateSecurityGroup — async update.
func (c *Client) UpdateSecurityGroup(ctx context.Context, req *vpcv1.UpdateSecurityGroupRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.SecurityGroups.Update(ctx, req, opts...)
}

// UpdateSecurityGroupRules — async batch-update правил (OCC через xmin внутри
// сервиса, см. CLAUDE.md §12 «Optimistic concurrency»).
func (c *Client) UpdateSecurityGroupRules(ctx context.Context, req *vpcv1.UpdateSecurityGroupRulesRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.SecurityGroups.UpdateRules(ctx, req, opts...)
}

// UpdateSecurityGroupRule — async update одного правила.
func (c *Client) UpdateSecurityGroupRule(ctx context.Context, req *vpcv1.UpdateSecurityGroupRuleRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.SecurityGroups.UpdateRule(ctx, req, opts...)
}

// DeleteSecurityGroup — async hard-delete. Default-SG (`default-sg-…`) защищён
// от удаления — сначала Network.Delete.
func (c *Client) DeleteSecurityGroup(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.SecurityGroups.Delete(ctx, &vpcv1.DeleteSecurityGroupRequest{SecurityGroupId: id}, opts...)
}

// MoveSecurityGroup — async folder-перемещение.
func (c *Client) MoveSecurityGroup(ctx context.Context, req *vpcv1.MoveSecurityGroupRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.SecurityGroups.Move(ctx, req, opts...)
}
