package tasks_test

import (
	"testing"

	"github.com/azylman/mirrormere/internal/tasks"
)

func TestChangeNotifier(t *testing.T) {
	t.Parallel()
	if d1, d2 := tasks.DefaultChangeNotifier(), tasks.DefaultChangeNotifier(); d1 == nil || d1 != d2 {
		t.Fatalf("expected singleton DefaultChangeNotifier, got d1=%v, d2=%v", d1, d2)
	}
	n := tasks.NewChangeNotifier()
	ch1, ch2 := make(chan struct{}, 2), make(chan struct{}, 2)
	unsub1, unsub2 := n.RegisterListener("list-a", ch1), n.RegisterListener("list-a", ch2)
	defer unsub2()

	n.NotifyChange("list-a")
	select {
	case <-ch1:
	default:
		t.Fatal("expected notification on ch1")
	}
	select {
	case <-ch2:
	default:
		t.Fatal("expected notification on ch2")
	}
	n.NotifyChange("list-a")
	n.NotifyChange("list-a")
	n.NotifyChange("")
	unsub1()
	for len(ch1) > 0 {
		<-ch1
	}
	n.NotifyChange("list-a")
	select {
	case <-ch1:
		t.Fatal("unexpected notification on ch1 after unsub")
	default:
	}
	func() {
		defer func() { _ = recover() }()
		var nilN *tasks.ChangeNotifier
		nilN.NotifyChange("list-a")
	}()
}
