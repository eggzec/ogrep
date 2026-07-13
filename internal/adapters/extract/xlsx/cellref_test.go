package xlsx

import "testing"

func TestParseCellRef(t *testing.T) {
	tests := []struct {
		ref    string
		col    int
		row    int
		wantOK bool
	}{
		{"A1", 1, 1, true},
		{"Z1", 26, 1, true},
		{"AA1", 27, 1, true},
		{"B45", 2, 45, true},
		{"AZ1", 52, 1, true}, // two-letter column beyond AA
		{"BA1", 53, 1, true},
		{"a1", 1, 1, true}, // lowercase should still parse
		{"", 0, 0, false},
		{"1", 0, 0, false},    // no column letters
		{"AB", 0, 0, false},   // no row digits
		{"A0", 0, 0, false},   // row 0 is invalid (1-based)
		{"A1B2", 0, 0, false}, // letters after digits: malformed
	}

	for _, tc := range tests {
		col, row, ok := parseCellRef(tc.ref)
		if ok != tc.wantOK {
			t.Errorf("parseCellRef(%q) ok = %v, want %v", tc.ref, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if col != tc.col || row != tc.row {
			t.Errorf("parseCellRef(%q) = (col=%d, row=%d), want (col=%d, row=%d)", tc.ref, col, row, tc.col, tc.row)
		}
	}
}

func TestColLetters(t *testing.T) {
	tests := []struct {
		col  int
		want string
	}{
		{1, "A"},
		{26, "Z"},
		{27, "AA"},
		{52, "AZ"},
		{53, "BA"},
	}
	for _, tc := range tests {
		got := colLetters(tc.col)
		if got != tc.want {
			t.Errorf("colLetters(%d) = %q, want %q", tc.col, got, tc.want)
		}
	}
}
