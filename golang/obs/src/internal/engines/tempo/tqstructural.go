package tempo

// Structural TraceQL operators over nested-set numbers.
// Copied verbatim from Tempo v3.1.1 (commit 9db7df79d2af)
// tempodb/encoding/vparquet5/block_traceql.go, lines 316-762, with the span
// type renamed to tqSpan (reached through tqAs) and its fields to left/right/nsParent.
// Copyright Grafana Labs, AGPL-3.0.

import (
	"sort"
	"unsafe"

	"github.com/grafana/tempo/pkg/traceql"
)

func (s *tqSpan) DescendantOf(lhs, rhs []traceql.Span, falseForAll, invert, union bool, buffer []traceql.Span) []traceql.Span {
	if len(lhs) == 0 && len(rhs) == 0 {
		return nil
	}

	if union {
		return tqDescendantOfUnion(lhs, rhs, invert, buffer)
	}

	// sort by nested set left. the goal is to quickly be able to find the first entry in the lhs slice that
	// potentially matches the rhs. after we find this first potential match we just check every single lhs
	// entry til the end of the slice.
	// it might be even better to clone the lhs slice. sort one by left and one by right and search the one that
	// requires less seeking after the search. this would be faster but cloning the slice would be costly in mem
	sortFn := func(i, j int) bool { return tqAs(lhs[i]).left > tqAs(lhs[j]).left } // sort asc b/c we are interested in lhs nestedSetLeft > rhs nestedSetLeft
	if invert {
		sortFn = func(i, j int) bool { return tqAs(lhs[i]).left < tqAs(lhs[j]).left } // sort desc b/c we want the inverse relationship. see descendantOf func
	}
	sort.Slice(lhs, sortFn)

	descendantOf := func(a, b *tqSpan) bool {
		if a.left == 0 ||
			b.left == 0 ||
			a.right == 0 ||
			b.right == 0 {
			// Spans with missing data, never a match.
			return false
		}
		return a.left > b.left && a.right < b.right
	}

	for _, r := range rhs {
		matches := false
		findFn := func(i int) bool { return tqAs(lhs[i]).left <= tqAs(r).left }
		if invert {
			findFn = func(i int) bool { return tqAs(lhs[i]).left >= tqAs(r).left }
		}

		// let's find the first index we need to bother with.
		found := sort.Search(len(lhs), findFn)
		if found == -1 { // if we are less then the entire slice we have to search the entire slice
			found = 0
		}

		for ; found < len(lhs); found++ {
			a := tqAs(lhs[found])
			b := tqAs(r)
			if invert {
				a, b = b, a
			}

			if descendantOf(b, a) {
				// Returns RHS
				matches = true
				break
			}
		}
		if matches && !falseForAll || // return RHS if there are any matches on the LHS
			!matches && falseForAll { // return RHS if there are no matches on the LHS
			buffer = append(buffer, r)
		}
	}

	return buffer
}

// descendantOfUnion is a special loop designed to handle union descendantOf. technically tqNestedSetManyManyLoop can logically do this
// but it contains a pathological case where it devolves to O(n^2) in the worst case (a trace with a single series of nested spans).
// this loop more directly handles union descendantof.
//   - iterate the rhs checking to see if it is a descendant of lhs
//   - break out of the rhs loop when the next span will be in a different branch
//   - at this point rSpan has the narrowest span (the leaf span) of the branch
//   - iterate the lhs as long as its in the same branch as rSpan checking for ancestors
//   - go back to rhs iteration and repeat until slices are exhausted
func tqDescendantOfUnion(lhs, rhs []traceql.Span, invert bool, buffer []traceql.Span) []traceql.Span {
	// union is harder b/c we have to find all matches on both the left and rhs
	sort.Slice(lhs, func(i, j int) bool { return tqAs(lhs[i]).left < tqAs(lhs[j]).left })
	if unsafe.SliceData(lhs) != unsafe.SliceData(rhs) { // if these are pointing to the same slice, no reason to sort again
		sort.Slice(rhs, func(i, j int) bool { return tqAs(rhs[i]).left < tqAs(rhs[j]).left })
	}

	isAfter := func(a, b *tqSpan) bool {
		return b.left > a.right
	}
	isMatch := func(a, d *tqSpan) bool {
		if d.left == 0 ||
			a.left == 0 ||
			d.right == 0 ||
			a.right == 0 {
			return false
		}
		return d.left > a.left && d.right < a.right
	}

	if invert {
		lhs, rhs = rhs, lhs
	}

	uniqueSpans := make(map[*tqSpan]struct{}) // todo: consider a reusable map, like our buffer slice
	addToBuffer := func(s *tqSpan) {
		if _, ok := uniqueSpans[s]; !ok {
			buffer = append(buffer, s)
			uniqueSpans[s] = struct{}{}
		}
	}

	lidx := 0
	ridx := 0
	for lidx < len(lhs) && ridx < len(rhs) {
		lSpan := tqAs(lhs[lidx])
		rSpan := tqAs(rhs[ridx])

		// rhs
		for ; ridx < len(rhs); ridx++ {
			rSpan = tqAs(rhs[ridx])

			if isMatch(lSpan, rSpan) {
				addToBuffer(rSpan)
			}

			// test the next span to see if it is still in the tree of lhs. if not bail!
			if ridx+1 < len(rhs) && isAfter(rSpan, tqAs(rhs[ridx+1])) {
				ridx++
				break
			}
		}

		// lhs
		// rSpan contains the narrowest span that is in tree for lhs. advance and add lhs until we're out of tree
		for ; lidx < len(lhs); lidx++ {
			lSpan = tqAs(lhs[lidx])

			// advance LHS until out of tree of RHS
			if isAfter(rSpan, lSpan) {
				break
			}

			if isMatch(lSpan, rSpan) {
				addToBuffer(lSpan)

				// if rSpan is in tree of lSpan keep on keeping on
				if ridx < len(rhs) && isMatch(lSpan, tqAs(rhs[ridx])) {
					break
				}
			}
		}
	}

	return buffer
}

// SiblingOf
func (s *tqSpan) SiblingOf(lhs, rhs []traceql.Span, falseForAll, union bool, buffer []traceql.Span) []traceql.Span {
	if len(lhs) == 0 && len(rhs) == 0 {
		return nil
	}

	if union {
		// union is more difficult b/c we have to find all matches on both the left and rhs
		sort.Slice(lhs, func(i, j int) bool { return tqAs(lhs[i]).nsParent < tqAs(lhs[j]).nsParent })
		if unsafe.SliceData(lhs) != unsafe.SliceData(rhs) { // if these are pointing to the same slice, no reason to sort again
			sort.Slice(rhs, func(i, j int) bool { return tqAs(rhs[i]).nsParent < tqAs(rhs[j]).nsParent })
		}

		siblingOf := func(a, b *tqSpan) bool {
			return a.nsParent == b.nsParent &&
				a.nsParent != 0 &&
				b.nsParent != 0 &&
				a != b // a span cannot be its own sibling. note that this only works due to implementation details in the engine. if we ever pipeline structural operators then we would need to use something else for identity. rownumber?
		}

		isValid := func(s *tqSpan) bool { return s.nsParent != 0 }
		isAfter := func(a, b *tqSpan) bool { return b.nsParent > a.nsParent }

		return tqNestedSetManyManyLoop(lhs, rhs, isValid, siblingOf, isAfter, falseForAll, false, union, buffer)
	}

	// this is easy. we're just looking for anything on the lhs side with the same nested set parent as the rhs
	sort.Slice(lhs, func(i, j int) bool {
		return tqAs(lhs[i]).nsParent < tqAs(lhs[j]).nsParent
	})

	siblingOf := func(a, b *tqSpan) bool {
		return a.nsParent == b.nsParent &&
			a.nsParent != 0 &&
			b.nsParent != 0
	}

	for _, r := range rhs {
		matches := false

		if tqAs(r).nsParent != 0 {
			// search for nested set parent
			found := sort.Search(len(lhs), func(i int) bool {
				return tqAs(lhs[i]).nsParent >= tqAs(r).nsParent
			})

			if found >= 0 && found < len(lhs) {
				matches = siblingOf(tqAs(r), tqAs(lhs[found]))

				// if we found a match BUT this is the same span as the match we need to check the very next span (if it exists).
				// this works b/c Search method returns the first match for nestedSetParent
				if matches && tqAs(r) == tqAs(lhs[found]) {
					matches = false
					if found+1 < len(lhs) {
						matches = siblingOf(tqAs(r), tqAs(lhs[found+1]))
					}
				}
			}
		}

		if matches && !falseForAll || // return RHS if there are any matches on the LHS
			!matches && falseForAll { // return RHS if there are no matches on the LHS
			buffer = append(buffer, r)
		}
	}
	return buffer
}

// {} > {}
func (s *tqSpan) ChildOf(lhs, rhs []traceql.Span, falseForAll, invert, union bool, buffer []traceql.Span) []traceql.Span {
	if len(lhs) == 0 && len(rhs) == 0 {
		return nil
	}

	if union {
		childOf := func(p, c *tqSpan) bool {
			return p.left == c.nsParent &&
				p.left != 0 &&
				c.nsParent != 0
		}
		isValid := func(s *tqSpan) bool { return s.left != 0 }
		isAfter := func(p, c *tqSpan) bool { return c.nsParent > p.left }

		// the engine will sometimes pass the same slice for both lhs and rhs. this occurs for {} > {}.
		// if lhs is the same slice as rhs we need to make a copy of the slice to sort them by different values
		if unsafe.SliceData(lhs) == unsafe.SliceData(rhs) {
			rhs = append([]traceql.Span{}, rhs...)
		}

		parents := lhs
		children := rhs
		if invert {
			parents, children = children, parents
		}

		sort.Slice(parents, func(i, j int) bool { return tqAs(parents[i]).left < tqAs(parents[j]).left })
		sort.Slice(children, func(i, j int) bool { return tqAs(children[i]).nsParent < tqAs(children[j]).nsParent })

		return tqNestedSetOneManyLoop(parents, children, isValid, childOf, isAfter, falseForAll, invert, union, buffer)
	}

	// we will search the LHS by either nestedSetLeft or nestedSetParent. if we are doing child we sort by nestedSetLeft
	// so we can quickly find children. if the invert flag is set we are looking for parents and so we sort appropriately
	sortFn := func(i, j int) bool { return tqAs(lhs[i]).left < tqAs(lhs[j]).left }
	if invert {
		sortFn = func(i, j int) bool { return tqAs(lhs[i]).nsParent < tqAs(lhs[j]).nsParent }
	}

	childOf := func(a, b *tqSpan) bool {
		return a.left == b.nsParent &&
			a.left != 0 &&
			b.nsParent != 0
	}

	sort.Slice(lhs, sortFn)
	for _, r := range rhs {
		findFn := func(i int) bool { return tqAs(lhs[i]).left >= tqAs(r).nsParent }
		if invert {
			findFn = func(i int) bool { return tqAs(lhs[i]).nsParent >= tqAs(r).left }
		}

		// search for nested set parent
		matches := false
		found := sort.Search(len(lhs), findFn)
		if found >= 0 && found < len(lhs) {
			if invert {
				matches = childOf(tqAs(r), tqAs(lhs[found])) // is the rhs a child of the lhs?
			} else {
				matches = childOf(tqAs(lhs[found]), tqAs(r)) // is the lhs a child of the rhs?
			}
		}

		if matches && !falseForAll || // return RHS if there are any matches on the LHS
			!matches && falseForAll { // return RHS if there are no matches on the LHS
			buffer = append(buffer, r)
		}
	}
	return buffer
}

// tqNestedSetOneManyLoop runs a standard one -> many loop to calculate nested set relationships. It handles all nested set relationships except
// siblingOf and unioned descendantOf. It forward iterates the one and many slices and applies.
func tqNestedSetOneManyLoop(one, many []traceql.Span, isValid func(*tqSpan) bool, isMatch, isAfter func(*tqSpan, *tqSpan) bool, falseForAll, invert, union bool, buffer []traceql.Span) []traceql.Span {
	var uniqueSpans map[*tqSpan]struct{}
	if union {
		uniqueSpans = make(map[*tqSpan]struct{}) // todo: consider a reusable map, like our buffer slice
	}

	addToBuffer := func(s *tqSpan) {
		if union {
			if _, ok := uniqueSpans[s]; !ok {
				buffer = append(buffer, s)
				uniqueSpans[s] = struct{}{}
			}
		} else {
			buffer = append(buffer, s)
		}
	}

	// note the small differences between this and the !invert loop. technically we could write these both in one piece of code,
	// but this feels better for clarity
	if invert {
		manyIdx := 0
		for _, o := range one {
			oSpan := tqAs(o)

			if !isValid(oSpan) {
				continue
			}

			matches := false
			for ; manyIdx < len(many); manyIdx++ {
				mSpan := tqAs(many[manyIdx])

				// if the many loop is ahead of the one loop break back to allow the one loop to let it catch up
				if isAfter(oSpan, mSpan) {
					break
				}

				if isMatch(oSpan, mSpan) {
					matches = true
					if union {
						addToBuffer(mSpan)
					} else {
						break
					}
				}
			}

			if (matches && !falseForAll) || (!matches && falseForAll) {
				addToBuffer(oSpan)
			}
		}

		return buffer
	}

	// !invert
	manyIdx := 0
	for _, o := range one {
		oSpan := tqAs(o)

		if !isValid(oSpan) {
			continue
		}

		matches := false
		for ; manyIdx < len(many); manyIdx++ {
			mSpan := tqAs(many[manyIdx])

			// if the many loop is ahead of the one loop break back to the allow one loop to let it catch up
			if isAfter(oSpan, mSpan) {
				break
			}

			match := isMatch(oSpan, mSpan)
			if (match && !falseForAll) || (!match && falseForAll) {
				matches = true
				addToBuffer(mSpan)
			}
		}

		if matches && union {
			addToBuffer(oSpan)
		}
	}

	// drain the rest of the children if falseForAll
	if falseForAll {
		for ; manyIdx < len(many); manyIdx++ {
			addToBuffer(tqAs(many[manyIdx]))
		}
	}

	return buffer
}

// tqNestedSetManyManyLoop handles the generic case when the lhs must be checked multiple times for each rhs. it is currently only
// used for siblingOf
func tqNestedSetManyManyLoop(lhs, rhs []traceql.Span, isValid func(*tqSpan) bool, isMatch, isAfter func(*tqSpan, *tqSpan) bool, falseForAll, invert, union bool, buffer []traceql.Span) []traceql.Span {
	var uniqueSpans map[*tqSpan]struct{}
	if union {
		uniqueSpans = make(map[*tqSpan]struct{}) // todo: consider a reusable map, like our buffer slice
	}

	addToBuffer := func(s *tqSpan) {
		if union {
			if _, ok := uniqueSpans[s]; !ok {
				buffer = append(buffer, s)
				uniqueSpans[s] = struct{}{}
			}
		} else {
			buffer = append(buffer, s)
		}
	}

	rescanIdx := 0
	lidx := 0
	for _, r := range rhs {
		rSpan := tqAs(r)
		if !isValid(rSpan) {
			continue
		}

		// rescan whatever amount of rhs we need to
		lidx = rescanIdx

		matches := false
		for ; lidx < len(lhs); lidx++ {
			lSpan := tqAs(lhs[lidx])

			// if left is after right, swap back to right
			if isAfter(rSpan, lSpan) {
				break
			}

			// if we transition forward (trees branches or parents or whatever) store current lidx to rescan
			if isAfter(tqAs(lhs[rescanIdx]), lSpan) {
				rescanIdx = lidx
			}

			if (!invert && isMatch(rSpan, lSpan)) || (invert && isMatch(lSpan, rSpan)) {
				matches = true
				if union {
					addToBuffer(lSpan)
				}
			}
		}

		if (matches && !falseForAll) || (!matches && falseForAll) {
			addToBuffer(rSpan)
		}
	}

	return buffer
}
