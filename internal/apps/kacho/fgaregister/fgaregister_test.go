// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package fgaregister

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Payload строки fga_register_outbox обязан нести labels + parent_project_id
// владельца-ресурса, чтобы kacho-iam материализовал их в resource_mirror для
// selector'а. Это форма leaf-домена, на которую опираются и repo-writer (emit),
// и clients-applier (forward).

// Payload должен round-trip'ить tuple + labels + parent + source_version.
func Test_T3_01_PayloadCarriesLabelsAndParent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := Payload{
		Tuple:           ProjectHierarchy("prj-P", "vpc_subnet", "sub-1"),
		Labels:          map[string]string{"env": "prod", "team": "core"},
		ParentProjectID: "prj-P",
		SourceVersion:   now,
	}
	b, err := Encode(p)
	require.NoError(t, err)

	got, err := Decode(b)
	require.NoError(t, err)
	assert.Equal(t, "project:prj-P", got.Tuple.SubjectID)
	assert.Equal(t, "project", got.Tuple.Relation)
	assert.Equal(t, "vpc_subnet:sub-1", got.Tuple.Object)
	assert.Equal(t, map[string]string{"env": "prod", "team": "core"}, got.Labels)
	assert.Equal(t, "prj-P", got.ParentProjectID)
	assert.True(t, got.SourceVersion.Equal(now), "source_version round-trips")
}

// Legacy-строки с голым Tuple (payload = только subject/relation/object) должны
// по-прежнему декодироваться в Payload с пустыми labels/parent — back-compat для
// строк, эмитированных раньше (они дренятся, не теряются).
func Test_T3_01_PayloadDecodesLegacyBareTuple(t *testing.T) {
	legacy := []byte(`{"subject_id":"project:prj-L","relation":"project","object":"vpc_network:net-L"}`)
	got, err := Decode(legacy)
	require.NoError(t, err)
	assert.Equal(t, "project:prj-L", got.Tuple.SubjectID)
	assert.Equal(t, "vpc_network:net-L", got.Tuple.Object)
	assert.Nil(t, got.Labels, "legacy row → no labels")
	assert.Empty(t, got.ParentProjectID)
	assert.True(t, got.SourceVersion.IsZero(), "legacy row → zero source_version (= -infinity in IAM)")
}

// Payload с неполным tuple — баг вызывающего: Valid() == false.
func Test_T3_01_PayloadValid(t *testing.T) {
	require.True(t, Payload{Tuple: ProjectHierarchy("p", "vpc_subnet", "sub-1")}.Valid())
	require.False(t, Payload{Tuple: Tuple{SubjectID: "project:p"}}.Valid(), "missing object/relation → invalid")
}

// Пустые labels (Create без labels) сериализуются без ключа labels (omitempty):
// IAM получает отсутствующую map, а не путаницу с литеральным null.
func Test_T3_01_PayloadOmitsEmptyLabels(t *testing.T) {
	p := Payload{Tuple: ProjectHierarchy("p", "vpc_subnet", "sub-1"), ParentProjectID: "p"}
	b, err := Encode(p)
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &raw))
	_, hasLabels := raw["labels"]
	assert.False(t, hasLabels, "empty labels omitted from payload")
}
