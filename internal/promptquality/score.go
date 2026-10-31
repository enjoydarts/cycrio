package promptquality

import (
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/ai"
	"math/big"
	"reflect"
)

type EventScore struct{ TP, FP, FN, KnownTotal, KnownCorrect, Hallucinated, ExpectedNull int }

func (s *EventScore) Add(v EventScore) {
	s.TP += v.TP
	s.FP += v.FP
	s.FN += v.FN
	s.KnownTotal += v.KnownTotal
	s.KnownCorrect += v.KnownCorrect
	s.Hallucinated += v.Hallucinated
	s.ExpectedNull += v.ExpectedNull
}
func equalFact(key string, a, b any) bool {
	if (key == "amount" || key == "recurring_amount") && a != nil && b != nil {
		x, ok := new(big.Rat).SetString(a.(string))
		y, yes := new(big.Rat).SetString(b.(string))
		return ok && yes && x.Cmp(y) == 0
	}
	return reflect.DeepEqual(a, b)
}
func facts(e ai.Event) map[string]any {
	b, _ := json.Marshal(e)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

// ScoreEvents matches service/type identities independent of ordering. Missing
// events lose every known fact; unsupported non-null fields count as inventions.
func ScoreEvents(gold, actual []ai.Event) EventScore {
	s := EventScore{}
	used := make([]bool, len(actual))
	for _, g := range gold {
		gm := facts(g)
		best := -1
		bestScore := -1
		for i, a := range actual {
			if used[i] || g.Service != a.Service || g.EventType != a.EventType {
				continue
			}
			am := facts(a)
			n := 0
			for k, v := range gm {
				if equalFact(k, v, am[k]) {
					n++
				}
			}
			if n > bestScore {
				best = i
				bestScore = n
			}
		}
		am := map[string]any{}
		if best < 0 {
			s.FN++
		} else {
			s.TP++
			used[best] = true
			am = facts(actual[best])
		}
		for k, v := range gm {
			if v != nil {
				s.KnownTotal++
				if best >= 0 && equalFact(k, v, am[k]) {
					s.KnownCorrect++
				}
			} else {
				s.ExpectedNull++
				if best >= 0 && am[k] != nil {
					s.Hallucinated++
				}
			}
		}
	}
	for i := range actual {
		if !used[i] {
			s.FP++
			for _, v := range facts(actual[i]) {
				if v != nil {
					s.Hallucinated++
				}
			}
		}
	}
	return s
}
