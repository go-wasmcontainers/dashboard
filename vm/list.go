package vm

import "github.com/go-widgets/mvvm"

// Sync makes l equal to want with as few list events as it can: rows are
// matched by key, a row that did not change emits nothing, a changed one
// one Set, a moved one one Move. A refresh that finds the engine as it was
// therefore costs no event and no redraw. Keys must be unique in want.
func Sync[T comparable](l *mvvm.ObservableList[T], want []T, key func(T) string) {
	keep := make(map[string]bool, len(want))
	for _, w := range want {
		keep[key(w)] = true
	}
	for i := l.Len() - 1; i >= 0; i-- {
		if !keep[key(l.At(i))] {
			l.RemoveAt(i)
		}
	}
	for i, w := range want {
		k := key(w)
		if i < l.Len() && key(l.At(i)) == k {
			if l.At(i) != w {
				l.Set(i, w)
			}
			continue
		}
		j := -1
		for x := i + 1; x < l.Len(); x++ {
			if key(l.At(x)) == k {
				j = x
				break
			}
		}
		if j < 0 {
			l.Insert(i, w)
			continue
		}
		l.Move(j, i)
		if l.At(i) != w {
			l.Set(i, w)
		}
	}
}
