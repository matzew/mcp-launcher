package main

import (
	"log"
	"net/http"
	"os"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/matzew/mcp-launcher/catalog"
	"github.com/matzew/mcp-launcher/handlers"
)

func main() {
	catalogNamespace := envOr("CATALOG_NAMESPACE", "mcp-catalog")
	targetNamespace := envOr("TARGET_NAMESPACE", "default")
	listenAddr := envOr("LISTEN_ADDR", ":8080")

	config, err := buildConfig()
	if err != nil {
		log.Fatalf("Failed to build kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create kubernetes client: %v", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create dynamic client: %v", err)
	}

	gwCfg := handlers.GatewayConfig{
		Enabled:          envOr("MCP_GATEWAY_ENABLED", "true") != "false",
		GatewayName:      envOr("MCP_GATEWAY_NAME", "mcp-gateway"),
		GatewayNamespace: envOr("MCP_GATEWAY_NAMESPACE", "gateway-system"),
	}

	store := catalog.NewStore(clientset, catalogNamespace)
	h := handlers.New(store, clientset, dynClient, targetNamespace, gwCfg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", h.Catalog)
	mux.HandleFunc("GET /configure/{name}", h.Configure)
	mux.HandleFunc("GET /preview/{name}", h.Preview)
	mux.HandleFunc("POST /run", h.Run)
	mux.HandleFunc("POST /deploy/{name}", h.QuickDeploy)
	mux.HandleFunc("GET /running", h.Running)
	mux.HandleFunc("DELETE /server/{namespace}/{name}", h.Delete)

	log.Printf("MCP Launcher listening on %s", listenAddr)
	log.Printf("Catalog namespace: %s, Target namespace: %s", catalogNamespace, targetNamespace)
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func buildConfig() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
