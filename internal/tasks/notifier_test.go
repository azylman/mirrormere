package tasks_test

import (
	"testing"

	"github.com/azylman/mirrormere/internal/tasks"
)

func TestChangeNotifier_DispatchAndUnregister(t *testing.T) {
	t.Parallel()

	n := tasks.NewChangeNotifier()
	ch1 := make(chan struct{}, 2)
	ch2 := make(chan struct{}, 2)

	unsub1 := n.RegisterListener("list-a", ch1)
	unsub2 := n.RegisterListener("list-a", ch2)
	chB := make(chan struct{}, 1)
	unsubB := n.RegisterListener("list-b", chB)
	defer unsubB()

	// Notify list-a
	n.NotifyChange("list-a")

	select {
	case <-ch1:
		// success
	default:
		t.Fatal("expected notification on ch1")
	}

	select {
	case <-ch2:
		// success
	default:
		t.Fatal("expected notification on ch2")
	}

	// Verify chB was not notified
	select {
	case <-chB:
		t.Fatal("unexpected notification on chB")
	default:
		// correct
	}

	// Test non-blocking behavior on full channel
	n.NotifyChange("list-a")
	n.NotifyChange("list-a")
	n.NotifyChange("list-a") // Channel buffer full, dropped gracefully

	// Test unsubscribe
	unsub1()
	unsub2()

	// Empty channels
	for len(ch1) > 0 {
		<-ch1
	}
	n.NotifyChange("list-a")
	select {
	case <-ch1:
		t.Fatal("ch1 received notification after unsubscribe")
	default:
		// correct
	}
}

func TestDefaultChangeNotifier(t *testing.T) {
	t.Parallel()

	d1 := tasks.DefaultChangeNotifier()
	d2 := tasks.DefaultChangeNotifier()
	if d1 == nil || d2 == nil || d1 != d2 {
		t.Fatalf("expected singleton DefaultChangeNotifier, got d1=%v, d2=%v", d1, d2)
	}
}
