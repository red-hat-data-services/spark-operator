# openshift/release CI Config for Spark Operator TLS-Scanner

This directory contains draft CI configuration files for the
[openshift/release](https://github.com/openshift/release) repository.

These files are NOT used by this repo directly. They are drafts to be
submitted as a PR to `openshift/release` under:

```
ci-operator/config/red-hat-data-services/spark-operator/
```

## Files

- `red-hat-data-services-spark-operator-main.yaml` — ci-operator config that
  builds the Spark Operator image, runs the RHOAI e2e tests on OpenShift, and
  executes the TLS-Scanner as a post-test step.

## References

- Parent epic: [RHOAIENG-95815](https://redhat.atlassian.net/browse/RHOAIENG-95815)
- Ticket: [RHOAIENG-95821](https://redhat.atlassian.net/browse/RHOAIENG-95821)
- Reference implementation: [openshift/release#82555](https://github.com/openshift/release/pull/82555)
- TLS-Scanner step: [tls-scanner-run-ref.yaml](https://github.com/openshift/release/blob/main/ci-operator/step-registry/tls/scanner/run/tls-scanner-run-ref.yaml)
