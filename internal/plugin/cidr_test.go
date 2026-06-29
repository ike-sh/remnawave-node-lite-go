package plugin

import (
	"reflect"
	"testing"
)

func TestNormalizeFilterPrefixes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		input  []string
		wantV4 []string
		wantV6 []string
	}{
		{
			name:   "merges contained ranges",
			input:  []string{"10.0.0.0/24", "10.0.0.128/25", "10.0.0.5"},
			wantV4: []string{"10.0.0.0/24"},
		},
		{
			name:   "merges adjacent halves",
			input:  []string{"10.0.0.0/25", "10.0.0.128/25"},
			wantV4: []string{"10.0.0.0/24"},
		},
		{
			name:   "dedupes plain ip",
			input:  []string{"1.1.1.1", "1.1.1.1", " 1.1.1.1 "},
			wantV4: []string{"1.1.1.1/32"},
		},
		{
			name:   "splits families and drops invalid",
			input:  []string{"bad", "", "::1", "8.8.8.8"},
			wantV4: []string{"8.8.8.8/32"},
			wantV6: []string{"::1/128"},
		},
		{
			name:   "merges ipv6 prefixes",
			input:  []string{"2001:db8::/33", "2001:db8:8000::/33"},
			wantV6: []string{"2001:db8::/32"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v4, v6 := normalizeFilterPrefixes(tc.input)
			if !equalStrings(v4, tc.wantV4) {
				t.Errorf("v4 = %v, want %v", v4, tc.wantV4)
			}
			if !equalStrings(v6, tc.wantV6) {
				t.Errorf("v6 = %v, want %v", v6, tc.wantV6)
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}
