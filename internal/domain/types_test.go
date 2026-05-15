package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Wave 2 pilot (KAC-99/KAC-94): unit-тесты domain newtypes Validate() — теперь
// валидация живёт в domain, а не в corevalidate.*; убеждаемся, что regex/length
// контракт verbatim YC сохранён.

func TestRcNameVPC_Validate(t *testing.T) {
	cases := []struct {
		name    string
		v       string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"simple", "net-a", false},
		{"uppercase allowed", "BadCAPS", false},
		{"underscore allowed", "abc_def", false},
		{"starts with digit forbidden", "1abc", true},
		{"starts with hyphen forbidden", "-abc", true},
		{"63 chars OK", "a" + strings.Repeat("b", 62), false},
		{"64 chars forbidden", "a" + strings.Repeat("b", 63), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domain.RcNameVPC(tc.v).Validate()
			if tc.wantErr {
				require.Error(t, err)
				st, _ := status.FromError(err)
				assert.Equal(t, codes.InvalidArgument, st.Code())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRcDescription_Validate(t *testing.T) {
	assert.NoError(t, domain.RcDescription("").Validate())
	assert.NoError(t, domain.RcDescription(strings.Repeat("a", 256)).Validate())
	err := domain.RcDescription(strings.Repeat("a", 257)).Validate()
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// UTF-8 rune count (не bytes): 256 ru-rune (по 2 байта) — ok.
	assert.NoError(t, domain.RcDescription(strings.Repeat("я", 256)).Validate())
	assert.Error(t, domain.RcDescription(strings.Repeat("я", 257)).Validate())
}

func TestLabelKey_Validate(t *testing.T) {
	assert.Error(t, domain.LabelKey("").Validate(), "empty key forbidden")
	assert.NoError(t, domain.LabelKey("env").Validate())
	assert.NoError(t, domain.LabelKey("tier-1").Validate())
	assert.Error(t, domain.LabelKey("Env").Validate(), "uppercase forbidden")
	assert.Error(t, domain.LabelKey("1env").Validate(), "starts with digit forbidden")
	assert.NoError(t, domain.LabelKey("env_v1").Validate())
	assert.NoError(t, domain.LabelKey("env.v1").Validate())
	assert.Error(t, domain.LabelKey(strings.Repeat("a", 64)).Validate(), ">63 chars forbidden")
}

func TestLabelVal_Validate(t *testing.T) {
	assert.NoError(t, domain.LabelVal("").Validate())
	assert.NoError(t, domain.LabelVal(strings.Repeat("a", 63)).Validate())
	assert.Error(t, domain.LabelVal(strings.Repeat("a", 64)).Validate(), ">63 chars forbidden")
}

func TestValidateLabels_Cardinality(t *testing.T) {
	// up to 64 — OK. Ключи фикс. 4 char: "k" + 3-char zero-padded index.
	good := map[string]string{}
	for i := 0; i < 64; i++ {
		key := "k"
		idx := i
		// 3-digit zero-pad
		for d := 100; d >= 1; d /= 10 {
			key += string(rune('0' + (idx/d)%10))
		}
		good[key] = "v"
	}
	require.Len(t, good, 64)
	require.NoError(t, domain.ValidateLabels(domain.LabelsFromMap(good)))

	// 65 — forbidden
	good["overflow"] = "v"
	assert.Error(t, domain.ValidateLabels(domain.LabelsFromMap(good)))
}

func TestNetwork_Validate_Composes(t *testing.T) {
	// happy
	n := domain.Network{
		Name:        domain.RcNameVPC("net1"),
		Description: domain.RcDescription("ok"),
		Labels:      domain.LabelsFromMap(map[string]string{"env": "prod"}),
	}
	assert.NoError(t, n.Validate())

	// bad name → InvalidArgument
	bad := domain.Network{Name: domain.RcNameVPC("1bad")}
	err := bad.Validate()
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
