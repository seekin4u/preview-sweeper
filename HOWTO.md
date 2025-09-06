maxsauce@192 ~/Desktop/preview-sweeper$ eval $(setup-envtest use -p env 1.30)
zsh: command not found: setup-envtest
maxsauce@192 ~/Desktop/preview-sweeper$ go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
go: sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20250731065915-e8c5c5445a20 requires go >= 1.24.0; switching to go1.24.6
maxsauce@192 ~/Desktop/preview-sweeper$ echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.zshrc
maxsauce@192 ~/Desktop/preview-sweeper$ source ~/.zshrc
maxsauce@192 ~/Desktop/preview-sweeper$ eval "$(setup-envtest use -p env 1.30.x)"
maxsauce@192 ~/Desktop/preview-sweeper$ go test ./internal/... -count=1    //integrated unit tests

//NOTE: do not forget about i use fucking x86 to build locally and cluster is arm64
maxsauce@192 ~/Desktop/preview-sweeper$ go test -tags=e2e ./test/e2e -v -count=1                 //tests on real cluster using my kubeconfig
---
Build:
ghcr-login (alias ghcr-login='security find-internet-password -s ghcr.io -w | docker login ghcr.io -u seekin4u --password-stdin')
export IMG=ghcr.io/seekin4u/preview-sweeper:v0.0.1
docker build -t $IMG -t "latest" -f Dockerfile .
docker push $IMG

//for arm64 arch
docker buildx create --use

//helm build
security find-internet-password -s ghcr.io -w | helm registry login ghcr.io -u seekin4u --password-stdin
~/preview-sweeper/charts/preview-sweeper$ helm dependency update
helm lint .
helm package .
helm push namespace-preview-sweeper-*.tgz oci://ghcr.io/seekin4u/helm
  
---
Helm:
First install monitoring: helm upgrade --install monitoring prometheus-community/kube-prometheus-stack -n monitoring --create-namespace --set prometheus.prometheusSpec.maximumStartupDurationSeconds=300

Locally: helm upgrade --install preview-sweeper ./charts/preview-sweeper -n namespace-sweeper --create-namespace -f ./charts/preview-sweeper/values.yaml
Remotely: helm upgrade --install preview-sweeper oci://ghcr.io/seekin4u/helm/preview-sweeper --version 0.1.1 -n namespace-sweeper --create-namespace -f values.yaml

Uninstall: helm uninstall preview-sweeper --namespace=namespace-sweeper

---
Kyverno: 
TESTING KYVERNO:
helm repo add kyverno https://kyverno.github.io/kyverno/
helm repo update
helm upgrade --install kyverno kyverno/kyverno -n kyverno --create-namespace
kubectl apply -f ./kyverno/ns-deletion-policy.yaml
