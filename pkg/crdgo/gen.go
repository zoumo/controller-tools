package crdgo

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/dave/jennifer/jen"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextlegacy "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-tools/pkg/crd"
	crdmarkers "sigs.k8s.io/controller-tools/pkg/crd/markers"
	"sigs.k8s.io/controller-tools/pkg/genall"
	"sigs.k8s.io/controller-tools/pkg/loader"
	"sigs.k8s.io/controller-tools/pkg/markers"
	"sigs.k8s.io/controller-tools/pkg/version"
)

// +controllertools:marker:generateHelp

// Generator generates CustomResourceDefinition objects.
type Generator struct {
	// TrivialVersions indicates that we should produce a single-version CRD.
	//
	// Single "trivial-version" CRDs are compatible with older (pre 1.13)
	// Kubernetes API servers.  The storage version's schema will be used as
	// the CRD's schema.
	//
	// Only works with the v1beta1 CRD version.
	TrivialVersions bool `marker:",optional"`

	// PreserveUnknownFields indicates whether or not we should turn off pruning.
	//
	// Left unspecified, it'll default to true when only a v1beta1 CRD is
	// generated (to preserve compatibility with older versions of this tool),
	// or false otherwise.
	//
	// It's required to be false for v1 CRDs.
	PreserveUnknownFields *bool `marker:",optional"`

	// AllowDangerousTypes allows types which are usually omitted from CRD generation
	// because they are not recommended.
	//
	// Currently the following additional types are allowed when this is true:
	// float32
	// float64
	//
	// Left unspecified, the default is false
	AllowDangerousTypes *bool `marker:",optional"`

	// MaxDescLen specifies the maximum description length for fields in CRD's OpenAPI schema.
	//
	// 0 indicates drop the description for all fields completely.
	// n indicates limit the description to at most n characters and truncate the description to
	// closest sentence boundary if it exceeds n characters.
	MaxDescLen *int `marker:",optional"`

	// CRDVersions specifies the target API versions of the CRD type itself to
	// generate.  Defaults to v1beta1.
	//
	// The first version listed will be assumed to be the "default" version and
	// will not get a version suffix in the output filename.
	//
	// You'll need to use "v1" to get support for features like defaulting,
	// along with an API server that supports it (Kubernetes 1.16+).
	CRDVersions []string `marker:"crdVersions,optional"`

	// HeaderFile specifies the header text (e.g. license) to prepend to generated files.
	HeaderFile string `marker:",optional"`
	// Year specifies the year to substitute for " YEAR" in the header file.
	Year string `marker:",optional"`
	// PakcageNae specifies the package name of genereated go file
	PackageName string `marker:"packageName"`
}

func (Generator) RegisterMarkers(into *markers.Registry) error {
	return crdmarkers.Register(into)
}

func (g Generator) Generate(ctx *genall.GenerationContext) error {
	if g.PackageName == "" {
		return fmt.Errorf("missing package name")
	}

	var headerText string

	if g.HeaderFile != "" {
		headerBytes, err := ctx.ReadFile(g.HeaderFile)
		if err != nil {
			return err
		}
		headerText = string(headerBytes)
	}
	headerText = strings.ReplaceAll(headerText, " YEAR", " "+g.Year)

	parser := &crd.Parser{
		Collector: ctx.Collector,
		Checker:   ctx.Checker,
		// Perform defaulting here to avoid ambiguity later
		AllowDangerousTypes: g.AllowDangerousTypes != nil && *g.AllowDangerousTypes == true,
	}

	crd.AddKnownTypes(parser)
	for _, root := range ctx.Roots {
		parser.NeedPackage(root)
	}

	metav1Pkg := crd.FindMetav1(ctx.Roots)
	if metav1Pkg == nil {
		// no objects in the roots, since nothing imported metav1
		return nil
	}

	// TODO: allow selecting a specific object
	kubeKinds := crd.FindKubeKinds(parser, metav1Pkg)
	if len(kubeKinds) == 0 {
		// no objects in the roots
		return nil
	}

	crdVersions := g.CRDVersions

	if len(crdVersions) == 0 {
		crdVersions = []string{"v1"}
	}

	cw := &codeWriter{
		packageName: g.PackageName,
		headerText:  headerText,
		parser:      parser,
		ctx:         ctx,
	}

	// crds := make(map[string][]*jen.Statement)
	crds := make(map[string][]interface{})

	for groupKind := range kubeKinds {
		parser.NeedCRDFor(groupKind, g.MaxDescLen)
		crdRaw := parser.CustomResourceDefinitions[groupKind]
		addAttribution(&crdRaw)

		versionedCRDs := make([]interface{}, len(crdVersions))
		for i, ver := range crdVersions {
			conv, err := crd.AsVersion(crdRaw, schema.GroupVersion{Group: apiext.SchemeGroupVersion.Group, Version: ver})
			if err != nil {
				return err
			}
			versionedCRDs[i] = conv
		}

		if g.TrivialVersions {
			for i, crd := range versionedCRDs {
				if crdVersions[i] == "v1beta1" {
					toTrivialVersions(crd.(*apiextlegacy.CustomResourceDefinition))
				}
			}
		}

		// *If* we're only generating v1beta1 CRDs, default to `preserveUnknownFields: (unset)`
		// for compatibility purposes.  In any other case, default to false, since that's
		// the sensible default and is required for v1.
		v1beta1Only := len(crdVersions) == 1 && crdVersions[0] == "v1beta1"
		switch {
		case (g.PreserveUnknownFields == nil || *g.PreserveUnknownFields) && v1beta1Only:
			crd := versionedCRDs[0].(*apiextlegacy.CustomResourceDefinition)
			crd.Spec.PreserveUnknownFields = nil
		case g.PreserveUnknownFields == nil, g.PreserveUnknownFields != nil && !*g.PreserveUnknownFields:
			// it'll be false here (coming from v1) -- leave it as such
		default:
			return fmt.Errorf("you may only set PreserveUnknownFields to true with v1beta1 CRDs")
		}

		for i, crd := range versionedCRDs {
			crds[crdVersions[i]] = append(crds[crdVersions[i]], crd)
		}
	}

	// generate zz.generated.scheme.go
	if err := cw.GenerateScheme(metav1Pkg); err != nil {
		return err
	}

	// genereate zz.generated.crds.go
	if err := cw.GenerateCrds(crds); err != nil {
		return err
	}
	return nil
}

func (Generator) CheckFilter() loader.NodeFilter {
	return filterTypesForCRDs
}

// filterTypesForCRDs filters out all nodes that aren't used in CRD generation,
// like interfaces and struct fields without JSON tag.
func filterTypesForCRDs(node ast.Node) bool {
	switch node := node.(type) {
	case *ast.InterfaceType:
		// skip interfaces, we never care about references in them
		return false
	case *ast.StructType:
		return true
	case *ast.Field:
		_, hasTag := loader.ParseAstTag(node.Tag).Lookup("json")
		// fields without JSON tags mean we have custom serialization,
		// so only visit fields with tags.
		return hasTag
	default:
		return true
	}
}

type codeWriter struct {
	packageName string
	headerText  string
	parser      *crd.Parser
	ctx         *genall.GenerationContext
}

func (cw *codeWriter) setFileDefault(f *jen.File) {
	f.HeaderComment("// +build !ignore_autogenerated\n")
	f.HeaderComment(cw.headerText + "\n")
	f.HeaderComment("// Code generated by controller-gen. DO NOT EDIT.")

	f.ImportAlias("k8s.io/apimachinery/pkg/apis/meta/v1", "metav1")
	f.ImportAlias("k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1", "apiextensionsv1beta1")
	f.ImportAlias("k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1", "apiextensionsv1")
}

func (cw *codeWriter) GenerateScheme(metav1Pkg *loader.Package) error {
	schemefile := jen.NewFile(cw.packageName)
	cw.setFileDefault(schemefile)

	scheme := jen.Qual("k8s.io/client-go/kubernetes/scheme", "Scheme")
	schemefile.Func().Id("init").Params().BlockFunc(func(g *jen.Group) {
		g.Qual("k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1", "AddToScheme").Call(scheme.Clone())
		g.Qual("k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1", "AddToScheme").Call(scheme.Clone())
		for pkg := range cw.parser.GroupVersions {
			if pkg == metav1Pkg {
				continue
			}
			g.Qual(loader.NonVendorPath(pkg.PkgPath), "AddToScheme").Call(scheme.Clone())
		}
	})

	w, err := cw.ctx.Open(nil, "zz.generated.scheme.go")
	if err != nil {
		return err
	}
	defer w.Close()
	if err := schemefile.Render(w); err != nil {
		return err
	}

	return nil
}

func (cw *codeWriter) GenerateCrds(crds map[string][]interface{}) error {
	crdsfile := jen.NewFile(cw.packageName)
	cw.setFileDefault(crdsfile)

	for version, list := range crds {
		values := []jen.Code{}
		for i := range list {
			values = append(values, GenerateValue(list[i]))
		}
		slice := jen.Index().Op("*").Qual("k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/"+version, "CustomResourceDefinition")
		crdsfile.Comment("//nolint")
		crdsfile.Func().Id("New" + Capitalize(version) + "CRDs").Params().Add(slice.Clone()).Block(
			jen.Return(
				slice.Clone().Values(values...),
			),
		)
	}

	writer, err := cw.ctx.Open(nil, "zz.generated.crds.go")
	if err != nil {
		return err
	}

	defer writer.Close()
	return crdsfile.Render(writer)
}

// toTrivialVersions strips out all schemata except for the storage schema,
// and moves that up into the root object.  This makes the CRD compatible
// with pre 1.13 clusters.
func toTrivialVersions(crd *apiextlegacy.CustomResourceDefinition) {
	var canonicalSchema *apiextlegacy.CustomResourceValidation
	var canonicalSubresources *apiextlegacy.CustomResourceSubresources
	var canonicalColumns []apiextlegacy.CustomResourceColumnDefinition
	for i, ver := range crd.Spec.Versions {
		if ver.Storage == true {
			canonicalSchema = ver.Schema
			canonicalSubresources = ver.Subresources
			canonicalColumns = ver.AdditionalPrinterColumns
		}
		crd.Spec.Versions[i].Schema = nil
		crd.Spec.Versions[i].Subresources = nil
		crd.Spec.Versions[i].AdditionalPrinterColumns = nil
	}
	if canonicalSchema == nil {
		return
	}

	crd.Spec.Validation = canonicalSchema
	crd.Spec.Subresources = canonicalSubresources
	crd.Spec.AdditionalPrinterColumns = canonicalColumns
}

// addAttribution adds attribution info to indicate controller-gen tool was used
// to generate this CRD definition along with the version info.
func addAttribution(crd *apiext.CustomResourceDefinition) {
	if crd.ObjectMeta.Annotations == nil {
		crd.ObjectMeta.Annotations = map[string]string{}
	}
	crd.ObjectMeta.Annotations["controller-gen.kubebuilder.io/version"] = version.Version()
}
