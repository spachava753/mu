package desktop

import "testing"

func TestFitSidebar(t *testing.T) {
	for _, tt := range []struct{ width, window, want float32 }{
		{0, 1100, sidebarDefaultWidth}, // nothing saved
		{300, 1100, 300},
		{700, 1100, 550},             // at most half the window
		{300, 500, 250},              // also when the window shrinks later
		{100, 1100, sidebarMinWidth}, // never narrower than the list reads
		{300, 200, sidebarMinWidth},  // the minimum wins in a tiny window
	} {
		if got := fitSidebar(tt.width, tt.window); got != tt.want {
			t.Errorf("fitSidebar(%v, %v) = %v, want %v", tt.width, tt.window, got, tt.want)
		}
	}
}

func TestSidebarDragSnaps(t *testing.T) {
	var d sidebarDrag
	d.start(232)
	if d.pointer != 232 || d.snapped || !d.active {
		t.Fatalf("start: %+v", d)
	}
	// Reaching the snap point is not enough.
	if d.move(sidebarSnapWidth) || d.snapped {
		t.Fatalf("snapped at the snap point: %+v", d)
	}
	if !d.move(sidebarSnapWidth-1) || !d.snapped {
		t.Fatalf("did not snap below it: %+v", d)
	}
	if d.move(20) {
		t.Fatal("crossed again while below")
	}
	// Coming back, the pointer must pass the release point, so that one resting
	// on the snap point cannot flicker.
	if d.move(sidebarSnapRelease-1) || !d.snapped {
		t.Fatalf("expanded before the release point: %+v", d)
	}
	if !d.move(sidebarSnapRelease) || d.snapped {
		t.Fatalf("did not expand at the release point: %+v", d)
	}
	// A drag starting on the rail starts collapsed, and one starting partway
	// through an animation goes by what shows.
	d = sidebarDrag{}
	d.start(railWidth)
	if d.pointer != railWidth || !d.snapped {
		t.Fatalf("rail start: %+v", d)
	}
	if !d.move(sidebarSnapRelease) || d.snapped {
		t.Fatalf("rail did not expand: %+v", d)
	}
	d = sidebarDrag{}
	d.start(330)
	if d.snapped || d.pointer != 330 {
		t.Fatalf("start partway: %+v", d)
	}
}
