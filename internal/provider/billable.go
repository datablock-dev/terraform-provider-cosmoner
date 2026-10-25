package provider

// What every billable resource shares: how `apply` waits for a resource to be
// ready or gone, and what a create that did not finish leaves in state.
//
// The decisions, also written down in CLAUDE.md:
//
//   - Create waits until the API reports the resource ready, polling its
//     status until the `timeouts.create` deadline. A resource another one
//     depends on is only useful once it accepts connections, and Terraform
//     would otherwise hand a half-provisioned host to the next resource.
//   - A create the API refuses leaves nothing behind: every create route
//     rolls back its own side effects before answering with an error.
//   - A resource that was created but failed to become ready (an error status,
//     or still provisioning at the deadline) is saved to state and reported as
//     an error, so Terraform marks it tainted. The provider never deletes it on
//     its own: a destroy should appear in a plan, a timed-out resource may yet
//     come up, and a failed one keeps its logs and status for diagnosis. The
//     next apply replaces it; `terraform destroy` removes it. Until then it is
//     billed, which the error says.
//   - When the create request itself fails in a way that leaves the outcome
//     unknown (no response, or a 5xx), the provider looks for the resource
//     before giving up, so a billed resource never exists outside state.
//   - Delete waits until the API no longer reports the resource.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

// pollInterval is the pause between status reads while waiting. The tests
// shorten it.
var pollInterval = 10 * time.Second

// errNotReady is returned by a wait check for a resource that ended up in a
// state it will not leave on its own.
type errNotReady struct {
	status string
	reason string
}

func (e *errNotReady) Error() string {
	if e.reason != "" {
		return fmt.Sprintf("status %s: %s", e.status, e.reason)
	}
	return "status " + e.status
}

// waitFor calls check until it reports done, returns an error, or timeout
// passes. check is called once straight away, so a resource that is already
// ready costs no pause.
func waitFor(ctx context.Context, timeout time.Duration, check func(context.Context) (bool, error)) error {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		done, err := check(deadline)
		if err != nil {
			if deadline.Err() != nil && ctx.Err() == nil {
				return fmt.Errorf("still not finished after %s", timeout)
			}
			return err
		}
		if done {
			return nil
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-deadline.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("still not finished after %s", timeout)
		case <-timer.C:
		}
	}
}

// outcomeUnknown reports whether a failed create may still have created the
// resource: no response at all, or a server error after the work began. A 4xx
// is the API refusing before it created anything.
func outcomeUnknown(err error) bool {
	var connErr *client.ConnectionError
	if errors.As(err, &connErr) {
		return true
	}
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Status >= http.StatusInternalServerError
}

// notReadyError reports a resource that was created but did not become ready.
// The caller has already saved it to state, which is what makes Terraform
// taint it.
func notReadyError(diags *diag.Diagnostics, kind string, err error) {
	diags.AddError(fmt.Sprintf("The %s was created but is not ready", kind),
		fmt.Sprintf("The %s was created, and is billed from creation, but did not become ready: %s.\n\n"+
			"Terraform has saved it as tainted: the next apply replaces it, and `terraform destroy` removes it. "+
			"Raise `timeouts.create` if it only needed longer.", kind, err))
}

// deleteWaitError reports a delete that was accepted but did not finish.
func deleteWaitError(diags *diag.Diagnostics, kind string, err error) {
	diags.AddError(fmt.Sprintf("The %s was not deleted", kind),
		fmt.Sprintf("The API accepted the deletion of the %s, but it still exists: %s. It stays in state; run the destroy again.", kind, err))
}
