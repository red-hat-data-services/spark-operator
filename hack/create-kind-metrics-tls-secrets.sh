#!/usr/bin/env bash
# Create a self-signed metrics CA, service-DNS certificates, and CA bundle for
# clusters that have no OpenShift service-ca operator. The RHOAI and ODH
# overlays mount these secrets as required volumes, so the files must be valid
# PEM or the metrics server will not start. Do not run this on OpenShift: the
# service-ca operator must create these resources from the Service annotations.
set -euo pipefail

namespace="${1:?namespace is required}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

openssl req -x509 -newkey rsa:2048 -sha256 -days 1 -nodes \
  -keyout "${tmpdir}/ca.key" \
  -out "${tmpdir}/ca.crt" \
  -subj "/CN=spark-operator-metrics-ca"

for component in controller webhook; do
  service_name="spark-operator-${component}-metrics"
  service_dns_name="${service_name}.${namespace}.svc"

  openssl req -new -newkey rsa:2048 -sha256 -nodes \
    -keyout "${tmpdir}/${component}.key" \
    -out "${tmpdir}/${component}.csr" \
    -subj "/CN=${service_dns_name}"

  printf 'subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\n' \
    "${service_dns_name}" > "${tmpdir}/${component}.ext"
  openssl x509 -req -sha256 -days 1 \
    -in "${tmpdir}/${component}.csr" \
    -CA "${tmpdir}/ca.crt" \
    -CAkey "${tmpdir}/ca.key" \
    -CAcreateserial \
    -out "${tmpdir}/${component}.crt" \
    -extfile "${tmpdir}/${component}.ext"

  kubectl create secret tls "${service_name}-certs" \
    --namespace="${namespace}" \
    --cert="${tmpdir}/${component}.crt" \
    --key="${tmpdir}/${component}.key" \
    --dry-run=client -o yaml | kubectl apply -f -
done

kubectl create configmap spark-operator-metrics-ca-bundle \
  --namespace="${namespace}" \
  --from-file=service-ca.crt="${tmpdir}/ca.crt" \
  --dry-run=client -o yaml | kubectl apply -f -
