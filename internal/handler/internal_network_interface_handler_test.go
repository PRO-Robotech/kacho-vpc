// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/nicinternal"
)

// Behaviour-level lock (apiconv malformed-id-first, S4-06): malformed nic_id → sync
// InvalidArgument с verbatim-текстом "invalid network interface id '<X>'" ПЕРВЫМ
// стейтментом — до обращения к сервису. Сервис-репо nil: если бы handler дошёл до
// него — паника (доказывает fast-fail до сервиса).
func TestInternalNIC_Attach_MalformedID_SyncInvalidArgument(t *testing.T) {
	h := NewInternalNetworkInterfaceHandler(nicinternal.NewService(nil))

	_, err := h.Attach(context.Background(), &vpcv1.AttachNetworkInterfaceRequest{
		NicId:      "bad-nic",
		InstanceId: "epdinst01",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "invalid network interface id 'bad-nic'", st.Message(),
		"verbatim contract-текст corevalidate.ResourceID (S4-06, Q7)")
}

// Detach — тот же malformed-id-first контракт.
func TestInternalNIC_Detach_MalformedID_SyncInvalidArgument(t *testing.T) {
	h := NewInternalNetworkInterfaceHandler(nicinternal.NewService(nil))

	_, err := h.Detach(context.Background(), &vpcv1.DetachNetworkInterfaceRequest{
		NicId:      "not-a-nic-id",
		InstanceId: "epdinst01",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "invalid network interface id 'not-a-nic-id'", st.Message())
}
