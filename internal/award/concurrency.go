package award

import "strconv"

const MaxConcurrency = 512

func ConcurrencyLimit(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return 4
	}
	if n > MaxConcurrency {
		return MaxConcurrency
	}
	return n
}
