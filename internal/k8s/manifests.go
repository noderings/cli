package k8s

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

// ApplyManifest applies a manifest from URL using server-side apply
func (c *Client) ApplyManifest(ctx context.Context, url string) error {
	// Download manifest from a configured/version-pinned upstream URL
	//nolint:gosec // G107: URL is supplied by install config (Calico/Liqo manifest pins)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download manifest: unexpected status %d", resp.StatusCode)
	}

	return c.applyManifestFromReader(ctx, resp.Body)
}

// ApplyManifestBytes applies a YAML/JSON manifest already in memory.
func (c *Client) ApplyManifestBytes(ctx context.Context, data []byte) error {
	return c.applyManifestFromReader(ctx, bytes.NewReader(data))
}

// ApplyManifestFromFile applies a manifest from a local file
func (c *Client) ApplyManifestFromFile(ctx context.Context, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open manifest file: %w", err)
	}
	defer func() { _ = file.Close() }()

	return c.applyManifestFromReader(ctx, file)
}

// applyManifestFromReader applies a manifest from a reader
func (c *Client) applyManifestFromReader(ctx context.Context, reader io.Reader) error {
	// Create dynamic client
	dynamicClient, err := dynamic.NewForConfig(c.config)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	// Create discovery client and REST mapper
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(c.config)
	if err != nil {
		return fmt.Errorf("create discovery client: %w", err)
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	// Decode YAML stream
	decoder := yamlutil.NewYAMLOrJSONDecoder(reader, 100)
	for {
		var rawObj runtime.RawExtension
		if err := decoder.Decode(&rawObj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode manifest: %w", err)
		}

		// Skip empty documents
		if len(rawObj.Raw) == 0 {
			continue
		}

		// Deserialize into unstructured object
		obj := &unstructured.Unstructured{}
		_, gvk, err := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme).Decode(rawObj.Raw, nil, obj)
		if err != nil {
			return fmt.Errorf("deserialize object: %w", err)
		}

		// Find REST mapping
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("find REST mapping for %s: %w", gvk, err)
		}

		// Get resource interface
		var dr dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			// Namespaced resource
			dr = dynamicClient.Resource(mapping.Resource).Namespace(obj.GetNamespace())
		} else {
			// Cluster-scoped resource
			dr = dynamicClient.Resource(mapping.Resource)
		}

		// Server-side apply
		obj.SetManagedFields(nil) // Remove managed fields for clean apply
		data, err := runtime.Encode(unstructured.UnstructuredJSONScheme, obj)
		if err != nil {
			return fmt.Errorf("encode object: %w", err)
		}

		force := true
		_, err = dr.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
			FieldManager: "nr-cli",
			Force:        &force,
		})
		if err != nil {
			return fmt.Errorf("apply %s %s/%s: %w", gvk, obj.GetNamespace(), obj.GetName(), err)
		}
	}

	return nil
}

// ApplyManifestsFromDirectory applies all YAML files from a directory
func (c *Client) ApplyManifestsFromDirectory(ctx context.Context, dirPath string) error {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("read directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Only process YAML/YML files
		ext := filepath.Ext(file.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		filePath := filepath.Join(dirPath, file.Name())
		if err := c.ApplyManifestFromFile(ctx, filePath); err != nil {
			return fmt.Errorf("apply manifest %s: %w", filePath, err)
		}
	}

	return nil
}
