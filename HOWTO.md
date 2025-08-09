maxsauce@192 ~/Desktop/preview-sweeper$ eval $(setup-envtest use -p env 1.30)                                                                                                         ✖ ✹main 
zsh: command not found: setup-envtest
maxsauce@192 ~/Desktop/preview-sweeper$ go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest                                                                          ✖ ✹main 
go: sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20250731065915-e8c5c5445a20 requires go >= 1.24.0; switching to go1.24.6
maxsauce@192 ~/Desktop/preview-sweeper$ echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.zshrc                                                                                   ✖ ✹main 
maxsauce@192 ~/Desktop/preview-sweeper$ source ~/.zshrc                                                                                                                               ✖ ✹main 
maxsauce@192 ~/Desktop/preview-sweeper$ eval "$(setup-envtest use -p env 1.30.x)"                                                                                                     ✖ ✹main 
maxsauce@192 ~/Desktop/preview-sweeper$ go test ./internal/... -count=1    