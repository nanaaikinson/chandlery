package odm

import "testing"

func TestSortableUpdateOne(t *testing.T) {
	t.Parallel()

	// MongoDB's own wire versions. 8.0 is the release that first accepts a
	// sort on updateOne; everything before it has to take the findAndModify
	// path instead.
	tests := []struct {
		name           string
		maxWireVersion int
		want           bool
	}{
		{"MongoDB 6.0", 17, false},
		{"MongoDB 7.0", 21, false},
		{"MongoDB 7.3", 24, false},
		{"MongoDB 8.0", 25, true},
		{"newer than 8.0", 26, true},
		{"unknown server", 0, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := sortableUpdateOne(test.maxWireVersion); got != test.want {
				t.Errorf("sortableUpdateOne(%d) = %t, want %t", test.maxWireVersion, got, test.want)
			}
		})
	}
}
