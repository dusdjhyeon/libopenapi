package v3

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/pb33f/libopenapi/datamodel"
	"github.com/pb33f/libopenapi/datamodel/low"
	"github.com/pb33f/libopenapi/datamodel/low/base"
	"github.com/pb33f/libopenapi/index"
	"github.com/pb33f/libopenapi/utils"
)

// CreateDocument will create a new Document instance from the provided SpecInfo.
//
// Deprecated: Use CreateDocumentFromConfig instead. This function will be removed in a later version, it
// defaults to allowing file and remote references, and does not support relative file references.
func CreateDocument(info *datamodel.SpecInfo) (*Document, error) {
	return createDocument(info, datamodel.NewDocumentConfiguration())
}

// CreateDocumentFromConfig Create a new document from the provided SpecInfo and DocumentConfiguration pointer.
func CreateDocumentFromConfig(info *datamodel.SpecInfo, config *datamodel.DocumentConfiguration) (*Document, error) {
	return createDocument(info, config)
}

func createDocument(info *datamodel.SpecInfo, config *datamodel.DocumentConfiguration) (*Document, error) {
	_, labelNode, versionNode := utils.FindKeyNodeFull(OpenAPILabel, info.RootNode.Content)
	var version low.NodeReference[string]
	if versionNode == nil {
		return nil, errors.New("no openapi version/tag found, cannot create document")
	}
	version = low.NodeReference[string]{Value: versionNode.Value, KeyNode: labelNode, ValueNode: versionNode}
	doc := Document{Version: version}

	// create an index config and shadow the document configuration.
	idxConfig := index.CreateClosedAPIIndexConfig()
	idxConfig.SpecInfo = info
	idxConfig.IgnoreArrayCircularReferences = config.IgnoreArrayCircularReferences
	idxConfig.IgnorePolymorphicCircularReferences = config.IgnorePolymorphicCircularReferences
	idxConfig.AvoidCircularReferenceCheck = true
	idxConfig.BaseURL = config.BaseURL
	idxConfig.BasePath = config.BasePath
	idxConfig.Logger = config.Logger
	rolodex := index.NewRolodex(idxConfig)
	rolodex.SetRootNode(info.RootNode)
	doc.Rolodex = rolodex

	// If basePath is provided, add a local filesystem to the rolodex.
	if idxConfig.BasePath != "" {
		var absError error
		var cwd string
		cwd, absError = filepath.Abs(config.BasePath)
		if absError != nil {
			return nil, absError
		}
		// if a supplied local filesystem is provided, add it to the rolodex.
		if config.LocalFS != nil {
			rolodex.AddLocalFS(cwd, config.LocalFS)
		} else {

			// create a local filesystem
			localFSConf := index.LocalFSConfig{
				BaseDirectory: cwd,
				DirFS:         os.DirFS(cwd),
				FileFilters:   config.FileFilter,
			}
			fileFS, err := index.NewLocalFSWithConfig(&localFSConf)
			if err != nil {
				return nil, err
			}
			idxConfig.AllowFileLookup = true

			// add the filesystem to the rolodex
			rolodex.AddLocalFS(cwd, fileFS)
		}

	}

	// if base url is provided, add a remote filesystem to the rolodex.
	if idxConfig.BaseURL != nil {

		// if a supplied remote filesystem is provided, add it to the rolodex.
		if config.RemoteFS != nil {
			if config.BaseURL == nil {
				return nil, errors.New("cannot use remote filesystem without a BaseURL")
			}
			rolodex.AddRemoteFS(config.BaseURL.String(), config.RemoteFS)

		} else {
			// create a remote filesystem
			remoteFS, fsErr := index.NewRemoteFSWithConfig(idxConfig)
			if fsErr != nil {
				return nil, fsErr
			}
			if config.RemoteURLHandler != nil {
				remoteFS.RemoteHandlerFunc = config.RemoteURLHandler
			}
			idxConfig.AllowRemoteLookup = true

			// add to the rolodex
			rolodex.AddRemoteFS(config.BaseURL.String(), remoteFS)
		}
	}

	// index the rolodex
	var errs []error

	// index all the things.
	_ = rolodex.IndexTheRolodex()

	// check for circular references
	if !config.SkipCircularReferenceCheck {
		rolodex.CheckForCircularReferences()
	}

	// extract errors
	roloErrs := rolodex.GetCaughtErrors()
	if roloErrs != nil {
		errs = append(errs, roloErrs...)
	}

	// set root index.
	doc.Index = rolodex.GetRootIndex()
	var wg sync.WaitGroup

	doc.Extensions = low.ExtractExtensions(info.RootNode.Content[0])

	// if set, extract jsonSchemaDialect (3.1)
	_, dialectLabel, dialectNode := utils.FindKeyNodeFull(JSONSchemaDialectLabel, info.RootNode.Content)
	if dialectNode != nil {
		doc.JsonSchemaDialect = low.NodeReference[string]{
			Value: dialectNode.Value, KeyNode: dialectLabel, ValueNode: dialectNode,
		}
	}

	runExtraction := func(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex,
		runFunc func(ctx context.Context, i *datamodel.SpecInfo, d *Document, idx *index.SpecIndex) error,
		ers *[]error,
		wg *sync.WaitGroup,
	) {
		if er := runFunc(ctx, info, doc, idx); er != nil {
			*ers = append(*ers, er)
		}
		wg.Done()
	}
	extractionFuncs := []func(ctx context.Context, i *datamodel.SpecInfo, d *Document, idx *index.SpecIndex) error{
		extractInfo,
		extractServers,
		extractTags,
		extractComponents,
		extractSecurity,
		extractExternalDocs,
		extractPaths,
		extractWebhooks,
	}

	ctx := context.Background()

	wg.Add(len(extractionFuncs))
	for _, f := range extractionFuncs {
		go runExtraction(ctx, info, &doc, rolodex.GetRootIndex(), f, &errs, &wg)
	}
	wg.Wait()
	return &doc, errors.Join(errs...)
}

func extractInfo(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFullTop(base.InfoLabel, info.RootNode.Content[0].Content)
	if vn != nil {
		ir := base.Info{}
		_ = low.BuildModel(vn, &ir)
		_ = ir.Build(ctx, ln, vn, idx)
		nr := low.NodeReference[*base.Info]{Value: &ir, ValueNode: vn, KeyNode: ln}
		doc.Info = nr
	}
	return nil
}

func extractSecurity(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	sec, ln, vn, err := low.ExtractArray[*base.SecurityRequirement](ctx, SecurityLabel, info.RootNode.Content[0], idx)
	if err != nil {
		return err
	}
	if vn != nil && ln != nil {
		doc.Security = low.NodeReference[[]low.ValueReference[*base.SecurityRequirement]]{
			Value:     sec,
			KeyNode:   ln,
			ValueNode: vn,
		}
	}
	return nil
}

func extractExternalDocs(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	extDocs, dErr := low.ExtractObject[*base.ExternalDoc](ctx, base.ExternalDocsLabel, info.RootNode.Content[0], idx)
	if dErr != nil {
		return dErr
	}
	doc.ExternalDocs = extDocs
	return nil
}

func extractComponents(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFullTop(ComponentsLabel, info.RootNode.Content[0].Content)
	if vn != nil {
		ir := Components{}
		_ = low.BuildModel(vn, &ir)
		err := ir.Build(ctx, vn, idx)
		if err != nil {
			return err
		}
		nr := low.NodeReference[*Components]{Value: &ir, ValueNode: vn, KeyNode: ln}
		doc.Components = nr
	}
	return nil
}

func extractServers(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFull(ServersLabel, info.RootNode.Content[0].Content)
	if vn != nil {
		if utils.IsNodeArray(vn) {
			var servers []low.ValueReference[*Server]
			for _, srvN := range vn.Content {
				if utils.IsNodeMap(srvN) {
					srvr := Server{}
					_ = low.BuildModel(srvN, &srvr)
					_ = srvr.Build(ctx, ln, srvN, idx)
					servers = append(servers, low.ValueReference[*Server]{
						Value:     &srvr,
						ValueNode: srvN,
					})
				}
			}
			doc.Servers = low.NodeReference[[]low.ValueReference[*Server]]{
				Value:     servers,
				KeyNode:   ln,
				ValueNode: vn,
			}
		}
	}
	return nil
}

func extractTags(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFull(base.TagsLabel, info.RootNode.Content[0].Content)
	if vn != nil {
		if utils.IsNodeArray(vn) {
			var tags []low.ValueReference[*base.Tag]
			for _, tagN := range vn.Content {
				if utils.IsNodeMap(tagN) {
					tag := base.Tag{}
					_ = low.BuildModel(tagN, &tag)
					if err := tag.Build(ctx, ln, tagN, idx); err != nil {
						return err
					}
					tags = append(tags, low.ValueReference[*base.Tag]{
						Value:     &tag,
						ValueNode: tagN,
					})
				}
			}
			doc.Tags = low.NodeReference[[]low.ValueReference[*base.Tag]]{
				Value:     tags,
				KeyNode:   ln,
				ValueNode: vn,
			}
		}
	}
	return nil
}

func extractPaths(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFull(PathsLabel, info.RootNode.Content[0].Content)
	if vn != nil {
		ir := Paths{}
		err := ir.Build(ctx, ln, vn, idx)
		if err != nil {
			return err
		}
		nr := low.NodeReference[*Paths]{Value: &ir, ValueNode: vn, KeyNode: ln}
		doc.Paths = nr
	}
	return nil
}

func extractWebhooks(ctx context.Context, info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	hooks, hooksL, hooksN, eErr := low.ExtractMap[*PathItem](ctx, WebhooksLabel, info.RootNode, idx)
	if eErr != nil {
		return eErr
	}
	if hooks != nil {
		doc.Webhooks = low.NodeReference[map[low.KeyReference[string]]low.ValueReference[*PathItem]]{
			Value:     hooks,
			KeyNode:   hooksL,
			ValueNode: hooksN,
		}
	}
	return nil
}
