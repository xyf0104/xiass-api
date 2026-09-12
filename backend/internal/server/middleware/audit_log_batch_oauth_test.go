package middleware

import "testing"

func TestBatchOAuthCredentialBodiesAreOmitted(t *testing.T) {
	for _, path := range []string{"POST /api/v1/admin/openai/batch-oauth/tasks", "POST /api/v1/admin/openai/batch-oauth/tasks/:task_id/restart"} {
		if _, ok := auditBodyOmittedRoutes[path]; !ok {
			t.Fatalf("credential route must omit request body: %s", path)
		}
	}
}
