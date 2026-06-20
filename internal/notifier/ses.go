package notifier

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// SESSender is the production EmailSender: it sends simple UTF-8 emails via SESv2.
type SESSender struct {
	client *sesv2.Client
	sender string // verified "From" address, from SENDER_EMAIL
}

// NewSESSender builds a SESSender from an SESv2 client and a verified sender address.
func NewSESSender(client *sesv2.Client, sender string) *SESSender {
	return &SESSender{client: client, sender: sender}
}

// Send delivers a single plaintext email. It builds a Simple message with UTF-8
// subject and body content and returns any error from the SES API unchanged.
func (s *SESSender) Send(ctx context.Context, to, subject, body string) error {
	_, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.sender),
		Destination: &types.Destination{
			ToAddresses: []string{to},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{
					Data:    aws.String(subject),
					Charset: aws.String("UTF-8"),
				},
				Body: &types.Body{
					Text: &types.Content{
						Data:    aws.String(body),
						Charset: aws.String("UTF-8"),
					},
				},
			},
		},
	})
	return err
}
