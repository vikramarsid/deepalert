package service

import (
	"encoding/json"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sfn"
	"github.com/m-mizutani/deepalert/internal/adaptor"
	"github.com/m-mizutani/deepalert/internal/errors"
)

// SFnService is utility to use AWS StepFunctions
type SFnService struct {
	newSFn adaptor.SFnClientFactory
}

// NewSFnService is constructor of SFnService
func NewSFnService(newSFn adaptor.SFnClientFactory) *SFnService {
	return &SFnService{
		newSFn: newSFn,
	}
}

// Exec invokes sfn.StartExecution with data
func (x *SFnService) Exec(arn string, data interface{}) *errors.Error {
	raw, err := json.Marshal(data)
	if err != nil {
		return errors.Wrap(err, "Fail to marshal report data")
	}

	region, daErr := extractSFnRegion(arn)
	if daErr != nil {
		return daErr
	}

	ssn := session.Must(session.NewSession(&aws.Config{
		Region: aws.String(region),
	}))
	svc := sfn.New(ssn)

	input := sfn.StartExecutionInput{
		Input:           aws.String(string(raw)),
		StateMachineArn: aws.String(arn),
	}

	if _, err := svc.StartExecution(&input); err != nil {
		return errors.Wrap(err, "Fail to execute state machine").With("arn", arn).With("data", string(raw))
	}

	return nil
}

func extractSFnRegion(arn string) (string, *errors.Error) {
	// arn sample: arn:aws:states:us-east-1:111122223333:stateMachine:machine-name
	arnParts := strings.Split(arn, ":")

	if len(arnParts) != 7 {
		return "", errors.New("Invalid state machine ARN").With("ARN", arn)
	}

	return arnParts[3], nil
}
