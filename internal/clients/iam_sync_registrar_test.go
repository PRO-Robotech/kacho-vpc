// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
)

func item(object string, labels map[string]string, parent string) fgaregister.Item {
	return fgaregister.Item{
		Tuple:           fgaregister.Tuple{SubjectID: "project:prj_x", Relation: "project", Object: object},
		Labels:          labels,
		ParentProjectID: parent,
	}
}

// Decision 2: SyncRegistrar.Register шлет RegisterResource на каждый Item с
// корректными полями (subject/relation/object/labels/parent + source_version).
func TestSyncRegistrar_Register_AllItems(t *testing.T) {
	fake := &fakeIAMRegisterClient{}
	reg := NewSyncRegistrar(fake)

	err := reg.Register(context.Background(), []fgaregister.Item{
		item("vpc_network:net1", map[string]string{"team": "core"}, "prj_x"),
		item("vpc_security_group:sg1", nil, ""),
	})
	require.NoError(t, err)

	require.Len(t, fake.registerCalls, 2)
	assert.Equal(t, "vpc_network:net1", fake.registerCalls[0].Object)
	assert.Equal(t, "project:prj_x", fake.registerCalls[0].SubjectId)
	assert.Equal(t, "project", fake.registerCalls[0].Relation)
	assert.Equal(t, "core", fake.registerCalls[0].Labels["team"])
	assert.Equal(t, "prj_x", fake.registerCalls[0].ParentProjectId)
	assert.NotNil(t, fake.registerCalls[0].SourceVersion, "source_version должен быть проставлен")
	assert.Equal(t, "vpc_security_group:sg1", fake.registerCalls[1].Object)
}

// Decision 2 fail-closed: ошибка RegisterResource пробрасывается наверх (первая
// ошибка прекращает регистрацию) → create-Operation завершится с ошибкой.
func TestSyncRegistrar_Register_FailClosed(t *testing.T) {
	fake := &fakeIAMRegisterClient{errSeq: []error{errors.New("iam unavailable")}}
	reg := NewSyncRegistrar(fake)

	err := reg.Register(context.Background(), []fgaregister.Item{item("vpc_network:net1", nil, "")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vpc_network:net1")
}

// Пустой список Item'ов → no-op, без ошибок.
func TestSyncRegistrar_Register_Empty(t *testing.T) {
	fake := &fakeIAMRegisterClient{}
	reg := NewSyncRegistrar(fake)
	require.NoError(t, reg.Register(context.Background(), nil))
	assert.Empty(t, fake.registerCalls)
}
