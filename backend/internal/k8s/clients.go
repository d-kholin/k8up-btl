package k8s

import (
        "fmt"
        "os"
        "path/filepath"

        "k8s.io/client-go/dynamic"
        "k8s.io/client-go/kubernetes"
        "k8s.io/client-go/rest"
        "k8s.io/client-go/tools/clientcmd"
)

// Clients wraps typed + dynamic access used by the GUI backend.
type Clients struct {
        Config    *rest.Config
        Typed     kubernetes.Interface
        Dynamic   dynamic.Interface
}

func New(kubeconfig string) (*Clients, error) {
        cfg, err := restConfig(kubeconfig)
        if err != nil {
                return nil, err
        }
        typed, err := kubernetes.NewForConfig(cfg)
        if err != nil {
                return nil, fmt.Errorf("typed client: %w", err)
        }
        dyn, err := dynamic.NewForConfig(cfg)
        if err != nil {
                return nil, fmt.Errorf("dynamic client: %w", err)
        }
        return &Clients{Config: cfg, Typed: typed, Dynamic: dyn}, nil
}

func restConfig(kubeconfig string) (*rest.Config, error) {
        if kubeconfig == "" {
                kubeconfig = os.Getenv("KUBECONFIG")
        }
        if kubeconfig == "" {
                if cfg, err := rest.InClusterConfig(); err == nil {
                        return cfg, nil
                }
                home, _ := os.UserHomeDir()
                kubeconfig = filepath.Join(home, ".kube", "config")
        }
        return clientcmd.BuildConfigFromFlags("", kubeconfig)
}
