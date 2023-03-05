// Copyright 2023 Princess B33f Heavy Industries / Dave Shanley
// SPDX-License-Identifier: MIT

package high

import (
    "github.com/pb33f/libopenapi/datamodel/low"
    "github.com/pb33f/libopenapi/datamodel/low/v3"
    "gopkg.in/yaml.v3"
    "reflect"
    "sort"
    "strconv"
    "strings"
    "unicode"
)

// NodeEntry represents a single node used by NodeBuilder.
type NodeEntry struct {
    Tag   string
    Key   string
    Value any
    Line  int
}

// NodeBuilder is a structure used by libopenapi high-level objects, to render themselves back to YAML.
// this allows high-level objects to be 'mutable' because all changes will be rendered out.
type NodeBuilder struct {
    Nodes []*NodeEntry
    High  any
    Low   any
}

// NewNodeBuilder will create a new NodeBuilder instance, this is the only way to create a NodeBuilder.
// The function accepts a high level object and a low level object (need to be siblings/same type).
//
// Using reflection, a map of every field in the high level object is created, ready to be rendered.
func NewNodeBuilder(high any, low any) *NodeBuilder {
    // create a new node builder
    nb := &NodeBuilder{High: high, Low: low}

    // extract fields from the high level object and add them into our node builder.
    // this will allow us to extract the line numbers from the low level object as well.
    v := reflect.ValueOf(high).Elem()
    num := v.NumField()
    for i := 0; i < num; i++ {
        nb.add(v.Type().Field(i).Name)
    }
    return nb
}

func (n *NodeBuilder) add(key string) {

    // only operate on exported fields.
    if unicode.IsLower(rune(key[0])) {
        return
    }

    // if the key is 'Extensions' then we need to extract the keys from the map
    // and add them to the node builder.
    if key == "Extensions" {
        extensions := reflect.ValueOf(n.High).Elem().FieldByName(key)
        for _, e := range extensions.MapKeys() {
            v := extensions.MapIndex(e)

            extKey := e.String()
            extValue := v.Interface()
            nodeEntry := &NodeEntry{Tag: extKey, Key: extKey, Value: extValue}

            if !reflect.ValueOf(n.Low).IsZero() {
                fieldValue := reflect.ValueOf(n.Low).Elem().FieldByName("Extensions")
                f := fieldValue.Interface()
                value := reflect.ValueOf(f)
                switch value.Kind() {
                case reflect.Map:
                    if j, ok := n.Low.(low.HasExtensionsUntyped); ok {
                        originalExtensions := j.GetExtensions()
                        for k := range originalExtensions {
                            if k.Value == extKey {
                                nodeEntry.Line = originalExtensions[k].ValueNode.Line
                            }
                        }
                    }
                default:
                    panic("not supported yet")
                }
            }
            n.Nodes = append(n.Nodes, nodeEntry)
        }
        // done, extensions are handled separately.
        return
    }

    // find the field with the tag supplied.
    field, _ := reflect.TypeOf(n.High).Elem().FieldByName(key)
    tag := string(field.Tag.Get("yaml"))
    tagName := strings.Split(tag, ",")[0]
    if tag == "-" {
        return
    }

    // extract the value of the field
    fieldValue := reflect.ValueOf(n.High).Elem().FieldByName(key)
    f := fieldValue.Interface()
    value := reflect.ValueOf(f)

    if f == nil || value.IsZero() {
        return
    }

    // create a new node entry
    nodeEntry := &NodeEntry{Tag: tagName, Key: key}

    switch value.Kind() {
    case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
        nodeEntry.Value = strconv.FormatInt(value.Int(), 10)
    case reflect.String:
        nodeEntry.Value = value.String()
    case reflect.Bool:
        nodeEntry.Value = value.Bool()
    case reflect.Slice:
        if tagName == v3.TypeLabel {
            if value.Len() == 1 {
                nodeEntry.Value = value.Index(0).String()
            }
        } else {
            if !value.IsNil() {
                nodeEntry.Value = f
            }
        }
    case reflect.Ptr:
        if !value.IsNil() {
            nodeEntry.Value = f
        }
    case reflect.Map:
        if !value.IsNil() {
            nodeEntry.Value = f
        }
    default:
        nodeEntry.Value = f
    }

    // if there is no low level object, then we cannot extract line numbers,
    // so skip and default to 0, which means a new entry to the spec.
    // this will place new content and the top of the rendered object.
    if !reflect.ValueOf(n.Low).IsZero() {
        lowFieldValue := reflect.ValueOf(n.Low).Elem().FieldByName(key)
        fLow := lowFieldValue.Interface()
        value = reflect.ValueOf(fLow)
        switch value.Kind() {
        case reflect.Struct:
            nb := value.Interface().(low.HasValueNodeUntyped).GetValueNode()
            if nb != nil {
                nodeEntry.Line = nb.Line
            }
        default:
            // everything else, weight it to the bottom of the rendered object.
            // this is things that we have no way of knowing where they should be placed.
            nodeEntry.Line = 9999
        }
    }
    if nodeEntry.Value != nil {
        n.Nodes = append(n.Nodes, nodeEntry)
    }
}

// Render will render the NodeBuilder back to a YAML node, iterating over every NodeEntry defined
func (n *NodeBuilder) Render() *yaml.Node {
    // order nodes by line number, retain original order
    sort.Slice(n.Nodes, func(i, j int) bool {
        return n.Nodes[i].Line < n.Nodes[j].Line
    })
    m := CreateEmptyMapNode()
    for i := range n.Nodes {
        node := n.Nodes[i]
        n.AddYAMLNode(m, node.Tag, node.Key, node.Value)
    }
    return m
}

// AddYAMLNode will add a new *yaml.Node to the parent node, using the tag, key and value provided.
// If the value is nil, then the node will not be added. This method is recursive, so it will dig down
// into any non-scalar types.
func (n *NodeBuilder) AddYAMLNode(parent *yaml.Node, tag, key string, value any) *yaml.Node {
    if value == nil {
        return parent
    }

    // check the type
    t := reflect.TypeOf(value)
    var l *yaml.Node
    if tag != "" {
        l = CreateStringNode(tag)
    }
    var valueNode *yaml.Node
    vo := reflect.ValueOf(value)
    switch t.Kind() {

    case reflect.String:
        val := value.(string)
        if val == "" {
            return parent
        }
        valueNode = CreateStringNode(val)
        break

    case reflect.Bool:
        val := value.(bool)
        if !val {
            return parent
        }
        valueNode = CreateBoolNode("true")
        break

    case reflect.Map:

        // the keys will be rendered randomly, if we don't find out the original line
        // number of the tag.
        var orderedCollection []*NodeEntry
        m := reflect.ValueOf(value)
        for _, k := range m.MapKeys() {

            var x string
            // extract key
            if o, ok := k.Interface().(low.HasKeyNode); ok {
                x = o.GetKeyNode().Value
            } else {
                x = k.String()
            }

            // go low and pull out the line number.
            lowProps := reflect.ValueOf(n.Low)
            if !lowProps.IsZero() && !lowProps.IsNil() {
                gu := lowProps.Elem()
                gi := gu.FieldByName(key)
                jl := reflect.ValueOf(gi)
                if !jl.IsZero() && gi.Interface() != nil {
                    gh := gi.Interface()
                    // extract low level key line number
                    if pr, ok := gh.(low.HasValueUnTyped); ok {
                        fg := reflect.ValueOf(pr.GetValueUntyped())
                        for _, ky := range fg.MapKeys() {
                            er := ky.Interface().(low.HasKeyNode).GetKeyNode().Value
                            if er == x {
                                orderedCollection = append(orderedCollection, &NodeEntry{
                                    Tag:   x,
                                    Key:   x,
                                    Line:  ky.Interface().(low.HasKeyNode).GetKeyNode().Line,
                                    Value: m.MapIndex(k).Interface(),
                                })
                            }
                        }
                    } else {
                        // this is a map, without an enclosing struct
                        bj := reflect.ValueOf(gh)
                        for _, ky := range bj.MapKeys() {
                            er := ky.Interface().(low.HasKeyNode).GetKeyNode().Value
                            if er == x {
                                orderedCollection = append(orderedCollection, &NodeEntry{
                                    Tag:   x,
                                    Key:   x,
                                    Line:  ky.Interface().(low.HasKeyNode).GetKeyNode().Line,
                                    Value: m.MapIndex(k).Interface(),
                                })
                            }
                        }
                    }
                } else {
                    // this is a map, without any low level details available (probably an extension map).
                    orderedCollection = append(orderedCollection, &NodeEntry{
                        Tag:   x,
                        Key:   x,
                        Line:  9999,
                        Value: m.MapIndex(k).Interface(),
                    })
                }
            } else {
                // this is a map, without any low level details available (probably an extension map).
                orderedCollection = append(orderedCollection, &NodeEntry{
                    Tag:   x,
                    Key:   x,
                    Line:  9999,
                    Value: m.MapIndex(k).Interface(),
                })
            }
        }

        // sort the slice by line number to ensure everything is rendered in order.
        sort.Slice(orderedCollection, func(i, j int) bool {
            return orderedCollection[i].Line < orderedCollection[j].Line
        })

        // create an empty map.
        p := CreateEmptyMapNode()

        // build out each map node in original order.
        for _, cv := range orderedCollection {
            n.AddYAMLNode(p, cv.Tag, cv.Key, cv.Value)
        }
        valueNode = p

    case reflect.Slice:
        if vo.IsNil() {
            return parent
        }

        var rawNode yaml.Node
        err := rawNode.Encode(value)
        if err != nil {
            return parent
        } else {
            valueNode = &rawNode
        }

    case reflect.Struct:
        if r, ok := value.(low.ValueReference[any]); ok {
            valueNode = r.GetValueNode()
        } else {
            panic("not supported yet")
        }

    case reflect.Ptr:
        if r, ok := value.(Renderable); ok {
            rawRender, _ := r.MarshalYAML()
            if rawRender != nil {
                valueNode = rawRender.(*yaml.Node)
            } else {
                return parent
            }
        } else {
            var rawNode yaml.Node
            err := rawNode.Encode(value)
            if err != nil {
                return parent
            } else {
                valueNode = &rawNode
            }
        }

    default:
        if vo.IsNil() {
            return parent
        }
        var rawNode yaml.Node
        err := rawNode.Encode(value)
        if err != nil {
            return parent
        } else {
            valueNode = &rawNode
        }
    }
    if l != nil {
        parent.Content = append(parent.Content, l, valueNode)
    } else {
        parent.Content = valueNode.Content
    }

    return parent
}

func CreateEmptyMapNode() *yaml.Node {
    n := &yaml.Node{
        Kind: yaml.MappingNode,
        Tag:  "!!map",
    }
    return n
}

func CreateStringNode(str string) *yaml.Node {
    n := &yaml.Node{
        Kind:  yaml.ScalarNode,
        Tag:   "!!str",
        Value: str,
    }
    return n
}

func CreateBoolNode(str string) *yaml.Node {
    n := &yaml.Node{
        Kind:  yaml.ScalarNode,
        Tag:   "!!bool",
        Value: str,
    }
    return n
}

type Renderable interface {
    MarshalYAML() (interface{}, error)
}
