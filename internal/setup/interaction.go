package setup

import "context"

type Step string

const (
	StepLogin       Step = "login"
	StepAccount     Step = "account"
	StepBucket      Step = "bucket"
	StepCredentials Step = "credentials"
	StepAccess      Step = "access"
	StepSave        Step = "save"
)

type Options struct {
	ConfigDir     string
	OAuthBaseURL  string
	OAuthClientID string
	APIBaseURL    string
	R2Endpoint    string
}

type Choice struct {
	Value       string
	Title       string
	Description string
}

type TextRequest struct {
	Label       string
	Secret      bool
	Validate    func(string) error
	Placeholder string
	Hint        string
	Length      int
}

type Interaction interface {
	Status(Step, string)
	Notice(string)
	Write([]byte) (int, error)
	Choose(context.Context, string, []Choice) (string, error)
	Text(context.Context, TextRequest) (string, error)
	ConfirmPublic(context.Context) (bool, error)
	Retry(context.Context, string) (bool, error)
}
