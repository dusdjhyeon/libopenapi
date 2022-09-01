package v3

import (
	"github.com/pb33f/libopenapi/datamodel"
	"github.com/pb33f/libopenapi/datamodel/low"
	"github.com/pb33f/libopenapi/datamodel/low/shared"
	"github.com/pb33f/libopenapi/index"
	"github.com/pb33f/libopenapi/resolver"
	"github.com/pb33f/libopenapi/utils"
	"sync"
)

func CreateDocument(info *datamodel.SpecInfo) (*Document, []error) {

	// clean state
	doc := Document{Version: low.ValueReference[string]{Value: info.Version, ValueNode: info.RootNode}}

	// build an index
	idx := index.NewSpecIndex(info.RootNode)
	doc.Index = idx

	// create resolver and check for circular references.
	resolve := resolver.NewResolver(idx)
	_ = resolve.CheckForCircularReferences()

	var wg sync.WaitGroup
	var errors []error

	doc.Extensions = low.ExtractExtensions(info.RootNode.Content[0])

	var runExtraction = func(info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex,
		runFunc func(i *datamodel.SpecInfo, d *Document, idx *index.SpecIndex) error,
		ers *[]error,
		wg *sync.WaitGroup) {

		if er := runFunc(info, doc, idx); er != nil {
			*ers = append(*ers, er)
		}
		wg.Done()
	}
	extractionFuncs := []func(i *datamodel.SpecInfo, d *Document, idx *index.SpecIndex) error{
		extractInfo,
		extractServers,
		extractTags,
		extractComponents,
		extractSecurity,
		extractExternalDocs,
		extractPaths,
	}

	wg.Add(len(extractionFuncs))
	for _, f := range extractionFuncs {
		go runExtraction(info, &doc, idx, f, &errors, &wg)
	}
	wg.Wait()
	return &doc, errors
}

func extractInfo(info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFull(shared.InfoLabel, info.RootNode.Content)
	if vn != nil {
		ir := shared.Info{}
		_ = low.BuildModel(vn, &ir)
		_ = ir.Build(vn, idx)
		nr := low.NodeReference[*shared.Info]{Value: &ir, ValueNode: vn, KeyNode: ln}
		doc.Info = nr
	}
	return nil
}

func extractSecurity(info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	sec, sErr := low.ExtractObject[*SecurityRequirement](SecurityLabel, info.RootNode, idx)
	if sErr != nil {
		return sErr
	}
	doc.Security = sec
	return nil
}

func extractExternalDocs(info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	extDocs, dErr := low.ExtractObject[*ExternalDoc](ExternalDocsLabel, info.RootNode, idx)
	if dErr != nil {
		return dErr
	}
	doc.ExternalDocs = extDocs
	return nil
}

func extractComponents(info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFull(ComponentsLabel, info.RootNode.Content)
	if vn != nil {
		ir := Components{}
		_ = low.BuildModel(vn, &ir)
		err := ir.Build(vn, idx)
		if err != nil {
			return err
		}
		nr := low.NodeReference[*Components]{Value: &ir, ValueNode: vn, KeyNode: ln}
		doc.Components = nr
	}
	return nil
}

func extractServers(info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFull(ServersLabel, info.RootNode.Content)
	if vn != nil {
		if utils.IsNodeArray(vn) {
			var servers []low.ValueReference[*Server]
			for _, srvN := range vn.Content {
				if utils.IsNodeMap(srvN) {
					srvr := Server{}
					_ = low.BuildModel(srvN, &srvr)
					_ = srvr.Build(srvN, idx)
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

func extractTags(info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFull(TagsLabel, info.RootNode.Content)
	if vn != nil {
		if utils.IsNodeArray(vn) {
			var tags []low.ValueReference[*Tag]
			for _, tagN := range vn.Content {
				if utils.IsNodeMap(tagN) {
					tag := Tag{}
					_ = low.BuildModel(tagN, &tag)
					if err := tag.Build(tagN, idx); err != nil {
						return err
					}
					tags = append(tags, low.ValueReference[*Tag]{
						Value:     &tag,
						ValueNode: tagN,
					})
				}
			}
			doc.Tags = low.NodeReference[[]low.ValueReference[*Tag]]{
				Value:     tags,
				KeyNode:   ln,
				ValueNode: vn,
			}
		}
	}
	return nil
}

func extractPaths(info *datamodel.SpecInfo, doc *Document, idx *index.SpecIndex) error {
	_, ln, vn := utils.FindKeyNodeFull(PathsLabel, info.RootNode.Content)
	if vn != nil {
		ir := Paths{}
		err := ir.Build(vn, idx)
		if err != nil {
			return err
		}
		nr := low.NodeReference[*Paths]{Value: &ir, ValueNode: vn, KeyNode: ln}
		doc.Paths = nr
	}
	return nil
}
