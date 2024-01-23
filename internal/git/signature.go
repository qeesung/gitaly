package git

import (
	"fmt"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// Rfc2822DateFormat is the date format that Git typically uses for dates.
	Rfc2822DateFormat = "Mon Jan 02 2006 15:04:05 -0700"
)

var signatureSanitizer = strings.NewReplacer("\n", "", "<", "", ">", "")

// Signature represents a commits signature.
type Signature struct {
	// Name of the author or the committer.
	Name string
	// Email of the author or the committer.
	Email string
	// When is the time of the commit.
	When time.Time
}

// NewSignature creates a new sanitized signature.
func NewSignature(name, email string, when time.Time) Signature {
	return Signature{
		Name:  signatureSanitizer.Replace(name),
		Email: signatureSanitizer.Replace(email),
		When:  when.Truncate(time.Second),
	}
}

// FormatTime formats the given time such that it can be used by Git.
//
// The formatted string uses RFC2822, which is typically used in the context of emails to format dates and which is well
// understood by Git in many contexts. This is _not_ usable though when you want to write a signature date directly into
// a Git object. In all other contexts, e.g. when passing a date via `GIT_COMMITTER_DATE`, it is preferable to use this
// format as it is unambiguous to Git. Unix timestamps are only recognized once they have at least 8 digits, which would
// thus rule all commit dates before 1970-04-26 17:46:40 +0000 UTC. While this is only ~4 months that we'd be missing
// since the birth of Unix timestamps, especially the zero date is likely going to come up frequently.
//
// If you need to format a time to be used in signatures directly, e.g. because it is passed to git-hash-object(1), you
// can use `FormatSignatureTime()` instead.
func FormatTime(t time.Time) string {
	return t.Format(Rfc2822DateFormat)
}

// FormatSignatureTime formats a time such that it can be embedded into a tag or commit object directly.
//
// This function should not be used in all other contexts. Refer to `FormatTime()` for the reasoning.
func FormatSignatureTime(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Unix(), t.Format("-0700"))
}

// RequestWithUserAndTimestamp represents a collection of requests that contains information used to
// generate a signature for user.
type RequestWithUserAndTimestamp interface {
	GetUser() *gitalypb.User
	GetTimestamp() *timestamppb.Timestamp
}

// SignatureFromRequest generates and returns a signature from the request, respecting the timezone
// information of the user.
func SignatureFromRequest(req RequestWithUserAndTimestamp) (Signature, error) {
	date := time.Now()

	if timestamp := req.GetTimestamp(); timestamp != nil {
		date = timestamp.AsTime()
	}

	user := req.GetUser()
	if user != nil {
		location, err := time.LoadLocation(user.GetTimezone())
		if err != nil {
			return Signature{}, err
		}
		date = date.In(location)
	}

	return NewSignature(string(user.GetName()), string(user.GetEmail()), date), nil
}
