package client

import (
	"time"
	"fmt"
	"regexp"
	"strings"
)

// BackupRateExceeded is an error which means that the
// rate limit on the backup API has been exceeded.
type BackupRateExceeded struct {
	retryAt time.Time
}

func (e BackupRateExceeded) Error() string {
	return fmt.Sprintf("Backup rate exceeded. Retry in %s", e.RetryIn())
}


func (e BackupRateExceeded) RetryIn() time.Duration {
	return e.retryAt.Sub(time.Now())
}

func (e BackupRateExceeded) RetryAt() time.Time {
	return e.retryAt
}

// FromResponse creates a BackupRateExceeded from the given text.
// It will attempt to extract the retry time from the text.
func (BackupRateExceeded) FromResponse(text string) BackupRateExceeded {

	// Helpfully Atlassian gives the response time in a format easily passed
	// to time.ParseDuration
	exp := regexp.MustCompile("((\\d+)h)?((\\d+)m)?")

	matches := exp.FindAllString(text, -1)
	durationStr := strings.Join(matches, "")

	dur, err := time.ParseDuration(durationStr)

	if err == nil {
		return BackupRateExceeded {
			retryAt: time.Now().Round(time.Minute).Add(dur),
		}
	}

	return BackupRateExceeded{retryAt: time.Now()}
}