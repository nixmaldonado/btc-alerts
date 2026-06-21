package notifier

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/google/go-cmp/cmp"
)

// img builds a NewImage/OldImage map with the given status. The PK carries the
// owner (OWNER#key123) so the handler can resolve the recipient; the item itself no
// longer holds an email. Pass status == "" to omit the status attribute entirely.
func img(status string) map[string]events.DynamoDBAttributeValue {
	m := map[string]events.DynamoDBAttributeValue{
		"PK":          events.NewStringAttribute("OWNER#key123"),
		"SK":          events.NewStringAttribute("ALERT#id1"),
		"direction":   events.NewStringAttribute("ABOVE"),
		"targetPrice": events.NewNumberAttribute("71000"),
		"firedAt":     events.NewStringAttribute("2026-06-20T12:00:00Z"),
	}
	if status != "" {
		m["status"] = events.NewStringAttribute(status)
	}
	return m
}

// fakeResolver is an in-memory EmailResolver: it maps owner id -> email and can be
// made to fail to exercise the transient-error path.
type fakeResolver struct {
	emails map[string]string
	err    error
}

func (r fakeResolver) GetOwnerEmail(_ context.Context, ownerID string) (string, bool, error) {
	if r.err != nil {
		return "", false, r.err
	}
	e, ok := r.emails[ownerID]
	return e, ok, nil
}

func record(eventName string, newImg, oldImg map[string]events.DynamoDBAttributeValue) events.DynamoDBEventRecord {
	return events.DynamoDBEventRecord{
		EventName: eventName,
		Change: events.DynamoDBStreamRecord{
			NewImage: newImg,
			OldImage: oldImg,
		},
	}
}

func TestShouldNotify(t *testing.T) {
	tests := []struct {
		name string
		rec  events.DynamoDBEventRecord
		want bool
	}{
		{"insert fired", record("INSERT", img("FIRED"), nil), true},
		{"modify armed to fired", record("MODIFY", img("FIRED"), img("ARMED")), true},
		{"modify fired to fired (no transition)", record("MODIFY", img("FIRED"), img("FIRED")), false},
		{"modify armed to armed", record("MODIFY", img("ARMED"), img("ARMED")), false},
		{"insert armed", record("INSERT", img("ARMED"), nil), false},
		{"remove fired", record("REMOVE", nil, img("FIRED")), false},
		{"new image missing status", record("INSERT", img(""), nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldNotify(tt.rec); got != tt.want {
				t.Errorf("shouldNotify(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestAlertEmail(t *testing.T) {
	tests := []struct {
		name string
		img  map[string]events.DynamoDBAttributeValue
		// wantSubject / wantBody are substring fragments asserted with strings.Contains.
		wantSubject []string
		wantBody    []string
	}{
		{
			name:        "formats a complete fired alert",
			img:         img("FIRED"),
			wantSubject: []string{"71000", "ABOVE"},
			wantBody:    []string{"71000", "ABOVE", "2026-06-20T12:00:00Z"},
		},
		{
			// An image with no direction/targetPrice/firedAt must not panic
			// (no nil-map deref): the call returns a possibly-empty string set.
			name: "missing attributes do not panic",
			img: map[string]events.DynamoDBAttributeValue{
				"PK": events.NewStringAttribute("OWNER#key123"),
				"SK": events.NewStringAttribute("ALERT#id1"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, body := alertEmail(tt.img)

			for _, want := range tt.wantSubject {
				if !strings.Contains(subject, want) {
					t.Errorf("subject = %q, want it to contain %q", subject, want)
				}
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(body, want) {
					t.Errorf("body = %q, want it to contain %q", body, want)
				}
			}
		})
	}
}

// recordingSender captures every Send call and can be made to fail.
type recordingSender struct {
	sent []sentEmail
	err  error
}

type sentEmail struct{ To, Subject, Body string }

func (r *recordingSender) Send(_ context.Context, to, subject, body string) error {
	r.sent = append(r.sent, sentEmail{to, subject, body})
	return r.err
}

func event(recs ...events.DynamoDBEventRecord) events.DynamoDBEvent {
	return events.DynamoDBEvent{Records: recs}
}

func TestHandle(t *testing.T) {
	sendErr := errors.New("ses down")
	resolveErr := errors.New("dynamo down")

	// sentMsg is the single email a FIRED img("FIRED") record produces, given the
	// owner resolves to user@example.com.
	sentMsg := sentEmail{
		To:      "user@example.com",
		Subject: "BTC Alert fired: ABOVE 71000",
		Body: "Your BTC price alert has fired.\n\n" +
			"Direction:    ABOVE\n" +
			"Target price: 71000 USD\n" +
			"Fired at:     2026-06-20T12:00:00Z\n",
	}

	tests := []struct {
		name        string
		rec         events.DynamoDBEventRecord
		sendErr     error // injected into the fake sender
		noProfile   bool  // resolver has no email for the owner
		resolverErr error // injected into the fake resolver
		want        []sentEmail
		wantErr     error // nil = success; matched with errors.Is
	}{
		{name: "insert fired sends", rec: record("INSERT", img("FIRED"), nil), want: []sentEmail{sentMsg}},
		{name: "modify armed to fired sends", rec: record("MODIFY", img("FIRED"), img("ARMED")), want: []sentEmail{sentMsg}},
		{name: "modify fired to fired no send", rec: record("MODIFY", img("FIRED"), img("FIRED")), want: nil},
		{name: "modify armed to armed no send", rec: record("MODIFY", img("ARMED"), img("ARMED")), want: nil},
		{name: "remove no send", rec: record("REMOVE", nil, img("FIRED")), want: nil},
		{name: "no profile email skips", rec: record("INSERT", img("FIRED"), nil), noProfile: true, want: nil},
		{name: "resolver error propagates", rec: record("INSERT", img("FIRED"), nil), resolverErr: resolveErr, want: nil, wantErr: resolveErr},
		{name: "send error propagates", rec: record("INSERT", img("FIRED"), nil), sendErr: sendErr, want: []sentEmail{sentMsg}, wantErr: sendErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &recordingSender{err: tt.sendErr}
			resolver := fakeResolver{emails: map[string]string{"key123": "user@example.com"}, err: tt.resolverErr}
			if tt.noProfile {
				resolver.emails = map[string]string{}
			}
			h := Handler{Sender: sender, Resolver: resolver}

			err := h.Handle(context.Background(), event(tt.rec))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Handle() err = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, sender.sent); diff != "" {
				t.Errorf("recorded emails mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
