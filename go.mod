module sigs.k8s.io/controller-tools

go 1.13

require (
	github.com/dave/jennifer v1.4.0
	github.com/fatih/color v1.7.0
	github.com/gobuffalo/flect v0.2.0
	github.com/google/go-cmp v0.4.0
	github.com/mattn/go-colorable v0.1.2 // indirect
	github.com/onsi/ginkgo v1.14.0
	github.com/onsi/gomega v1.10.1
	github.com/spf13/cobra v1.0.0
	github.com/spf13/pflag v1.0.5
	github.com/zoumo/golib v0.0.0-20200810084958-ee8ff56b8a54
	golang.org/x/tools v0.0.0-20200616195046-dc31b401abb5
	gopkg.in/yaml.v3 v3.0.0-20190905181640-827449938966
	k8s.io/api v0.18.2
	k8s.io/apiextensions-apiserver v0.18.2
	k8s.io/apimachinery v0.18.2
	sigs.k8s.io/yaml v1.2.0
)

replace github.com/dave/jennifer => github.com/zoumo/jennifer v1.4.1-0.20200901083333-7511cf6cea96
