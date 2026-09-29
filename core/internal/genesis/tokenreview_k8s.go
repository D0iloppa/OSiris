package genesis

import (
	"context"
	"fmt"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// K8sTokenReviewer is the production TokenReviewer (genesis.go's seam),
// calling `tokenreviews.authentication.k8s.io` exactly as
// docs/design/70-kubernetes-deployment.md §7.1 describes:
//
//	"이것은 인증 검증 권한이지 워크로드 생성 권한이 아니다 … TokenReview 호출은
//	'제출된 토큰이 유효한 K8s 신원인가'를 K8s API 서버에 묻는 것일 뿐 그 자체로
//	아무 리소스도 만들거나 바꾸지 않는다."
//
// This type therefore needs only a `create` RBAC verb on `tokenreviews`, and
// §7.1 asks that this be stated in code, not only in docs, because "core가
// 워크로드를 만들 수 있게 됐다"로 오독할 가능성" is real: nothing here
// constructs, patches, or deletes any Pod/Deployment/Agent object.
type K8sTokenReviewer struct {
	clientset kubernetes.Interface
	// audiences, if non-empty, is passed through to TokenReviewSpec.Audiences
	// so a token minted for a different audience is refused at the API
	// server rather than accepted here and refused one line later — K8s can
	// enforce this more precisely than a string compare in this package
	// could.
	audiences []string
}

// NewK8sTokenReviewer wires a real cluster client. main.go (this build's one
// approved wiring point for concrete infrastructure, mirroring
// orchestrator's bin/lint-driver-boundary.sh convention) is expected to
// construct `clientset` from in-cluster config (rest.InClusterConfig()) —
// this package does not do that discovery itself, so genesis stays testable
// without a cluster (see the fake TokenReviewer used by genesis_test.go).
func NewK8sTokenReviewer(clientset kubernetes.Interface, audiences ...string) *K8sTokenReviewer {
	return &K8sTokenReviewer{clientset: clientset, audiences: audiences}
}

// Review implements TokenReviewer. It parses the response's
// `status.user.username`, which for a bound ServiceAccount token has the
// fixed shape `system:serviceaccount:<namespace>:<name>` (70 §4.1 names this
// literal string) — there is no separate namespace/name field on the
// response, so parsing this one string IS how the identity is recovered.
func (r *K8sTokenReviewer) Review(ctx context.Context, token string) (Identity, error) {
	review := &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token:     token,
			Audiences: r.audiences,
		},
	}
	result, err := r.clientset.AuthenticationV1().TokenReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return Identity{}, fmt.Errorf("tokenreviews.authentication.k8s.io create: %w", err)
	}
	if result.Status.Error != "" {
		return Identity{}, fmt.Errorf("token review error: %s", result.Status.Error)
	}
	if !result.Status.Authenticated {
		return Identity{}, nil // Authenticated=false, not an error — Guard.Verify turns this into ErrUnauthenticated
	}

	ns, sa, ok := parseServiceAccountUsername(result.Status.User.Username)
	if !ok {
		// Authenticated, but not a ServiceAccount at all (e.g. a user
		// certificate) — Guard.Verify's identity match will refuse this via
		// ErrWrongIdentity since ns/sa will not equal the expected values.
		return Identity{Authenticated: true}, nil
	}
	uid := ""
	if u, present := result.Status.User.Extra["authentication.kubernetes.io/credential-id"]; present && len(u) > 0 {
		uid = u[0]
	}
	return Identity{Authenticated: true, Namespace: ns, ServiceAccount: sa, UID: uid}, nil
}

// parseServiceAccountUsername splits "system:serviceaccount:<ns>:<name>".
func parseServiceAccountUsername(username string) (namespace, name string, ok bool) {
	const prefix = "system:serviceaccount:"
	if !strings.HasPrefix(username, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(username, prefix)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
