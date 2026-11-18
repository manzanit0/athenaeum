package meta

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// numericDate matches a date written with the year first, such as
// "2019", "2019-03" or "2019-03-12", with whatever time follows it.
var numericDate = regexp.MustCompile(`^(\d{4})(?:[-/.](\d{1,2})(?:[-/.](\d{1,2}))?)?(?:[T ].*)?$`)

// anyYear finds a year between 1000 and 2999 in running text.
var anyYear = regexp.MustCompile(`\b[12]\d{3}\b`)

// writtenDates lists the layouts of dates written with the name of the
// month, each with whether it names a day.
var writtenDates = []struct {
	// layout is the time layout to parse with.
	layout string
	// day says whether the layout holds a day.
	day bool
}{
	{"January 2006", false}, {"Jan 2006", false},
	{"2 January 2006", true}, {"2 Jan 2006", true},
	{"January 2, 2006", true}, {"Jan 2, 2006", true},
	{time.RFC1123, true}, {time.RFC1123Z, true},
}

// real reports whether the year, month and day make a day that exists.
func real(year, month, day int) bool {
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return t.Year() == year && int(t.Month()) == month && t.Day() == day
}

// numeric returns the date written in the groups matched by numericDate.
func numeric(groups []string) (string, bool) {
	year, _ := strconv.Atoi(groups[1])
	if groups[2] == "" {
		return groups[1], true
	}
	month, _ := strconv.Atoi(groups[2])
	if groups[3] == "" {
		return fmt.Sprintf("%04d-%02d", year, month), month >= 1 && month <= 12
	}
	day, _ := strconv.Atoi(groups[3])
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day), real(year, month, day)
}

// PartialDate returns the date written in s as a year, a year and month
// or a full day, in the forms "2006", "2006-01" and "2006-01-02", and
// whether s holds such a date. Nothing at all counts as no date.
func PartialDate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	if groups := numericDate.FindStringSubmatch(s); groups != nil {
		return numeric(groups)
	}
	for _, written := range writtenDates {
		t, err := time.Parse(written.layout, s)
		if err != nil {
			continue
		}
		if written.day {
			return t.Format("2006-01-02"), true
		}
		return t.Format("2006-01"), true
	}
	return "", false
}

// Date returns the date found in s, which a file wrote in a form of its
// own, as PartialDate does. Text that is not a date but names a year
// gives that year, and anything else gives no date.
func Date(s string) string {
	if date, ok := PartialDate(s); ok {
		return date
	}
	return anyYear.FindString(s)
}
