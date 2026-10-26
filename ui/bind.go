package ui

import (
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/toolkit"
)

// bindRows keeps t.Rows equal to l, cell by cell, applying each list event
// as it comes (a changed container rewrites one row, not the table), and
// calls invalidate after each.
func bindRows[T any](t *toolkit.Table, l *mvvm.ObservableList[T], cells func(T) []string, invalidate func()) {
	t.Rows = t.Rows[:0]
	for i := 0; i < l.Len(); i++ {
		t.Rows = append(t.Rows, cells(l.At(i)))
	}
	l.Subscribe(func(ev mvvm.ListEvent[T]) {
		switch ev.Kind {
		case mvvm.ListInsert:
			add := make([][]string, len(ev.Items))
			for i, it := range ev.Items {
				add[i] = cells(it)
			}
			t.Rows = append(t.Rows[:ev.Index], append(add, t.Rows[ev.Index:]...)...)
		case mvvm.ListRemove:
			t.Rows = append(t.Rows[:ev.Index], t.Rows[ev.Index+ev.Count:]...)
		case mvvm.ListReplace:
			t.Rows[ev.Index] = cells(ev.Items[0])
		case mvvm.ListMove:
			r := t.Rows[ev.Index]
			t.Rows = append(t.Rows[:ev.Index], t.Rows[ev.Index+1:]...)
			t.Rows = append(t.Rows[:ev.To], append([][]string{r}, t.Rows[ev.To:]...)...)
		case mvvm.ListReset:
			t.Rows = t.Rows[:0]
			for i := 0; i < l.Len(); i++ {
				t.Rows = append(t.Rows, cells(l.At(i)))
			}
		}
		invalidate()
	})
}

// bindItems keeps a ListBox's items equal to l.
func bindItems[T any](lb *toolkit.ListBox, l *mvvm.ObservableList[T], text func(T) string, invalidate func()) {
	sync := func() {
		lb.Items = lb.Items[:0]
		for i := 0; i < l.Len(); i++ {
			lb.Items = append(lb.Items, text(l.At(i)))
		}
		invalidate()
	}
	sync()
	l.SubscribeChanged(sync)
}

// bindLabel keeps a Label's text equal to f() whenever one of the sources
// changes.
func bindLabel(l *toolkit.Label, f func() string, invalidate func(), sources ...mvvm.Changeable) {
	set := func() {
		l.Text().Set(f())
		invalidate()
	}
	set()
	for _, s := range sources {
		s.SubscribeChanged(set)
	}
}
