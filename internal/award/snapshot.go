package award

func copyPointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

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
			f.TaxesUSD = copyPointer(f.TaxesUSD)
			f.TaxEstimate = copyPointer(f.TaxEstimate)
			if f.TaxEstimate != nil {
				f.TaxEstimate.USD = copyPointer(f.TaxEstimate.USD)
				f.TaxEstimate.Components = append([]TaxComponent(nil), f.TaxEstimate.Components...)
			}
		}
	}
	return r
}
