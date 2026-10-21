package award

func copyPointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// CloneResult isolates a published preview from subsequent provider mutations.
func CloneResult(r Result) Result {
	r.CacheExpiresAt = copyPointer(r.CacheExpiresAt)
	r.Warnings = append([]string{}, r.Warnings...)
	r.Itineraries = append([]Itinerary{}, r.Itineraries...)
	for x := range r.Itineraries {
		i := &r.Itineraries[x]
		i.Segments = append([]Segment(nil), i.Segments...)
		for y := range i.Segments {
			i.Segments[y].Aircraft = copyPointer(i.Segments[y].Aircraft)
		}
		i.Fares = append([]Fare(nil), i.Fares...)
		for y := range i.Fares {
			f := &i.Fares[y]
			f.Seats = copyPointer(f.Seats)
			f.CashPrice = copyPointer(f.CashPrice)
			f.MilesCash = copyPointer(f.MilesCash)
			f.MilesCashEligible = copyPointer(f.MilesCashEligible)
		}
	}
	return r
}
