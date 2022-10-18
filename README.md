# Knative Serving Affinity Demo

This repository demonstrates the _revision affinity_ and _session affinity_
support for Knative Serving under development at
https://github.com/tardieu/serving/tree/affinity.

## Overview

Revision affinity makes it possible for a series of requests to be consistently
routed to the same revision of a Knative service as long as this revision
remains a valid target. Requests in the series must include the same _flow id_
using a request header, query parameter, or path parameter. When Knative Serving
receives a request with an unknown flow id, it selects the target revision using
the standard decision procedure. When Knative Serving receives a request with a
known flow id, it sticks to the already selected target revision if still valid.
Otherwise, a new revision is selected.

Session affinity makes it possible for a series of requests to be consistently
routed to the same replica of a Knative service as long as the pod remains a
valid target. Requests in the series must include the same _session id_ using a
request header, query parameter, or path parameter. When Knative Serving
receives a request with an unknown session id, it selects the target revision
and target pod for the target revision using the standard decision procedure.
When Knative Serving receives a request with a known session id, it sticks to
the already selected target pod if still valid. Otherwise, a new pod is
selected.

Session affinity implies revision affinity. If the selected pod is no longer a
valid target but the selected revision remains a valid target, Knative Serving
sticks to the already selected revision for the session.

Revision and session affinity take precedence over other routing mechanisms such
as traffic splitting.

Session affinity does not prevent Knative Serving to scale down a service,
possibly forcing the selection of a new target pod for an ongoing session.

This prototype affinity implementation is built into the Knative Serving
activator component. It requires the activator to always remain on the request
path for services that make use of revision or session affinity. The activator
component may be replicated. We use Redis to consistently handle affinity across
activator replicas.

## Example stateful service

To test affinity, we implement a small stateful service in
[affinity.go](affinity.go). This service keeps track of the number of requests
per session.

```bash
git clone https://github.com/tardieu/affinity.git
cd affinity

go run affinity.go &

curl 'localhost:8080/incr?session_id=abc'
: session=abc, count=1
curl 'localhost:8080/incr?session_id=abc'
: session=abc, count=2
curl 'localhost:8080/incr?session_id=123'
: session=123, count=1
curl 'localhost:8080/incr?session_id=abc'
: session=abc, count=3
curl 'localhost:8080/incr'
: session=, count=1
curl 'localhost:8080/incr'
: session=, count=2

kill %1
```

Requests without a session id increment the session count for the empty session
id.

## Setup the Knative cluster

First install [Kind](https://kind.sigs.k8s.io), the [Knative
CLI](https://knative.dev/docs/client/install-kn/), and the [Quickstart
plugin](https://knative.dev/docs/getting-started/quickstart-install/) for the
Knative CLI. For instance, on macOS via Homebrew:

```bash
brew install kind
brew install knative/client/kn
brew install knative-sandbox/kn-plugins/quickstart
```

Create a Kind cluster and deploy Knative Serving.

```bash
kn quickstart kind --install-serving
```

Patch the autoscaler configuration to keep the activator in the request path.

```bash
kubectl patch configmap/config-autoscaler -n knative-serving -p '{"data":{"target-burst-capacity": "-1"}}'
```

Deploy Redis.

```bash
kubectl apply -n knative-serving -f redis.yaml
```

Override the default activator image to add support for session and revision
affinity.

```bash
kubectl set image -n knative-serving deployment activator activator=quay.io/tardieu/activator:dev
```

Optionally replicate the activator.

```bash
kubectl patch hpa activator -n knative-serving -p '{"spec":{"minReplicas":2,"maxReplicas":20}}'
```

## Single replica

Deploy a single replica of the service.

```bash
kn service create affinity --image quay.io/tardieu/affinity:dev --scale 1

curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=1
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=2
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=123'
: session=123, count=1
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=3
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=123'
: session=123, count=2
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=123'
: session=123, count=3
```

With a single replica of a single revision, session counts behave as expected.

## Session affinity

To experiment with session affinity, we will scale the example service to three
replica.

### Default behavior: no session affinity

Deploy three replicas of the service.

```bash
kn service delete affinity
kn service create affinity --image quay.io/tardieu/affinity:dev --scale 3
```

By default Knative Serving does not understand sessions. Requests for the same
session may be routed to several distinct replicas. Each replica maintains its
own session counts so the "aggregate" session count returned for a given session
may appear to stall or decrease depending on the distribution of requests among
replicas.

```bash
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=1
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=1
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=2
```

### Session affinity using a request header

Redeploy three replicas to reset session counts.

```bash
kn service delete affinity
kn service create affinity --image quay.io/tardieu/affinity --scale 3
```

Requests with the same `K-Session` header value are routed to the same replica.

```bash
curl -H 'K-Session: abc' 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=1
curl -H 'K-Session: abc' 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=2
curl -H 'K-Session: abc' 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=3
```

A custom header name may be specified using the
`activator.knative.dev/sticky-session-query-parameter` service annotation.

### Session affinity using query parameter

Deploy three replicas with `activator.knative.dev/sticky-session-header-name`
service annotation.
```bash
kn service delete affinity
kn service create affinity --image quay.io/tardieu/affinity:dev --scale 3 -a activator.knative.dev/sticky-session-query-parameter=session_id
```

Knative Serving now extracts the session id directly from the request url
without the need for an additional header.

```bash
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=1
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=2
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?session_id=abc'
: session=abc, count=3
```

### Session affinity using a path parameter

A session id may also be provided as a path parameter. Suppose for instance we
revise the example service to accept requests of the form `/incr/session_id`. We
can use annotation `activator.knative.dev/sticky-session-path-segment=2` to
instruct Knative Serving to recognize the second segment of the request path as
the session id.

## Revision affinity


To experiment with revision affinity, we will deploy two revisions of the
example service. We will deploy a single replica per revision so we can get
proper session counts within a revision without having to rely on session
affinity.

### Default behavior: no revision affinity

Deploy two revisions of the service with a single replica per revision and a
50/50 traffic split.

```bash
kn service delete affinity
kn service create affinity --image quay.io/tardieu/affinity:dev --scale 1
kn service update affinity --scale 1 --traffic affinity-00001=50 --traffic @latest=50
```

By default Knative Serving splits traffic among revisions as specified. Requests
may therefore increment the session count for either revision 1 or revision 2
(for the empty session id). This "aggregate" session count may appear to stall
or decrease.

```bash
curl 'http://affinity.default.127.0.0.1.sslip.io/incr'
: session=, count=1
curl 'http://affinity.default.127.0.0.1.sslip.io/incr'
: session=, count=1
curl 'http://affinity.default.127.0.0.1.sslip.io/incr'
: session=, count=2
```

### Revision affinity using a request header

Redeploy two revisions to reset session counts.

```bash
kn service delete affinity
kn service create affinity --image quay.io/tardieu/affinity:dev --scale 1
kn service update affinity --scale 1 --traffic affinity-00001=50 --traffic @latest=50
```

Requests with the same `K-Revision` header value are routed to the same revision
and increment the same session count since we deployed a single replica per
revision.

```bash
curl -H 'K-Revision: abc' 'http://affinity.default.127.0.0.1.sslip.io/incr'
: session=, count=1
curl -H 'K-Revision: abc' 'http://affinity.default.127.0.0.1.sslip.io/incr'
: session=, count=2
curl -H 'K-Revision: abc' 'http://affinity.default.127.0.0.1.sslip.io/incr'
: session=, count=3
```

A custom header name may be specified using the
`activator.knative.dev/sticky-revision-header-name` service annotation.

### Revision affinity using a query parameter

Deploy two replicas with `activator.knative.dev/sticky-revision-query-parameter`
service annotation.

```bash
kn service delete affinity
kn service create affinity --image quay.io/tardieu/affinity:dev --scale 1
kn service update affinity --scale 1 --traffic affinity-00001=50 --traffic @latest=50 -a activator.knative.dev/sticky-revision-query-parameter=flow_id
```

Knative Serving now extracts the flow id from the request url.

```bash
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?flow_id=abc'
: session=, count=1
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?flow_id=abc'
: session=, count=2
curl 'http://affinity.default.127.0.0.1.sslip.io/incr?flow_id=abc'
: session=, count=3
```

### Revision affinity using a path parameter

A flow id may also be provided as a path parameter using the
`activator.knative.dev/sticky-revision-path-segment` service annotation.


## Cleanup

To cleanup simply delete the Kind cluster:

```bash
kind delete cluster --name knative
```
