package service

import "context"

type openAIStream403AccountRepo struct {
	AccountRepository
	setErrorCalls int
}

func (r *openAIStream403AccountRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}
