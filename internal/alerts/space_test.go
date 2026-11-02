// SPDX-License-Identifier: AGPL-3.0-or-later

package alerts

import (
	"strings"
	"testing"
)

func TestSpaceThresholdsAndHysteresis(t *testing.T) {
	const gb = 1_000_000_000
	level := func(percent uint64, active map[string]string) string {
		out := EvaluateSpace([]SpaceInput{{Pool: "tank", UsedBytes: percent * gb, SizeBytes: 100 * gb}}, active)
		if len(out) == 0 {
			return ""
		}
		if out[0].ID != "pool.space:tank" || out[0].Code != "pool.space" || out[0].Params["pool"] != "tank" {
			t.Fatalf("condition = %+v", out[0])
		}
		return out[0].Level
	}
	for _, tc := range []struct {
		percent uint64
		active  string
		want    string
	}{
		{84, "", ""}, {85, "", Warn}, {94, "", Warn}, {95, "", Crit}, {100, "", Crit},
		// Once raised, an alert falls back only 2 points below its threshold.
		{84, Warn, Warn}, {83, Warn, Warn}, {82, Warn, ""},
		{94, Crit, Crit}, {93, Crit, Crit}, {92, Crit, Warn}, {82, Crit, ""},
	} {
		if got := level(tc.percent, map[string]string{"pool.space:tank": tc.active}); got != tc.want {
			t.Errorf("%d %% with %q active: %q, want %q", tc.percent, tc.active, got, tc.want)
		}
	}

	// The system disk has its own id; an unknown size raises nothing.
	out := EvaluateSpace([]SpaceInput{{UsedBytes: 30 * gb, SizeBytes: 31 * gb}, {Pool: "e2"}}, nil)
	if len(out) != 1 || out[0].ID != "system.space" || out[0].Level != Crit || out[0].Params["freeBytes"] != uint64(gb) {
		t.Fatalf("system disk = %+v", out)
	}
	// The notification says the threshold and what is left, in the admin's units.
	if got := Title("system.space", "fr", out[0].Params); !strings.Contains(got, "95 %") || !strings.Contains(got, "1,0 Go") {
		t.Errorf("fr title = %q", got)
	}
	if got := Title("system.space", "en", map[string]any{"threshold": 85, "freeBytes": float64(2_500_000_000)}); !strings.Contains(got, "2.5 GB left") {
		t.Errorf("en title (params from JSON) = %q", got)
	}
}
