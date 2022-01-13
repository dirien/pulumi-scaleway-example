package main

import (
	"github.com/jaxxstorm/pulumi-scaleway/sdk/go/scaleway"
	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes"
	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apiextensions"
	v1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	rbac "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/rbac/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ProviderDependency struct {
	ctx         *pulumi.Context
	provider    pulumi.ProviderResource
	certManager pulumi.Resource
	ingress     pulumi.Resource
}

func (p *ProviderDependency) createDex() error {
	dex, err := helm.NewRelease(p.ctx, "dex", &helm.ReleaseArgs{
		Name:            pulumi.String("dex"),
		Chart:           pulumi.String("dex"),
		Version:         pulumi.String("0.6.5"),
		Namespace:       pulumi.String("dex"),
		CreateNamespace: pulumi.Bool(true),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String("https://charts.dexidp.io"),
		},
		Values: pulumi.Map{
			"config": pulumi.Map{
				"issuer": pulumi.String("https://dex.ediri.cloud/dex"),
				"storage": pulumi.Map{
					"type": pulumi.String("memory"),
				},
				"web": pulumi.Map{
					"http": pulumi.String("0.0.0.0:5556"),
					"frontend": pulumi.Map{
						"theme":     pulumi.String("coreos"),
						"issuer":    pulumi.String("ediri.cloud"),
						"issuerUrl": pulumi.String("https://dex.ediri.cloud"),
					},
				},
				"connectors": pulumi.Array{
					pulumi.Map{
						"type": pulumi.String("github"),
						"id":   pulumi.String("github"),
						"name": pulumi.String("GitHub"),
						"config": pulumi.Map{
							"clientID":     config.RequireSecret(p.ctx, "clientId"),
							"clientSecret": config.RequireSecret(p.ctx, "clientSecret"),
							"redirectURI":  pulumi.String("https://dex.ediri.cloud/dex/callback"),
							"useLoginAsID": pulumi.Bool(true),
						},
					},
				},
				"staticClients": pulumi.Array{
					pulumi.Map{
						"id":     pulumi.String("kubernetes"),
						"name":   pulumi.String("Kubernetes Cluster Authentication"),
						"secret": pulumi.String("password"),
						"redirectURIs": pulumi.Array{
							pulumi.String("http://localhost:8000"),
						},
					},
				},
			},
			"ingress": pulumi.Map{
				"className": pulumi.String("nginx"),
				"hosts": pulumi.Array{
					pulumi.Map{
						"host": pulumi.String("dex.ediri.cloud"),
						"paths": pulumi.Array{
							pulumi.Map{
								"path":     pulumi.String("/"),
								"pathType": pulumi.String("ImplementationSpecific"),
							},
						},
					},
				},
				"enabled": pulumi.Bool(true),
				"tls": pulumi.Array{
					pulumi.Map{
						"hosts": pulumi.Array{
							pulumi.String("dex.ediri.cloud"),
						},
						"secretName": pulumi.String("dex-tls"),
					},
				},
				"annotations": pulumi.Map{
					"external-dns.alpha.kubernetes.io/hostname": pulumi.String("dex.ediri.cloud"),
					"external-dns.alpha.kubernetes.io/ttl":      pulumi.String("60"),
				},
			},
		},
	}, pulumi.Provider(p.provider))
	if err != nil {
		return err
	}
	_, err = apiextensions.NewCustomResource(p.ctx, "dex-certificate", &apiextensions.CustomResourceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("dex-certificate"),
			Namespace: pulumi.String("dex"),
		},
		ApiVersion: pulumi.String("cert-manager.io/v1"),
		Kind:       pulumi.String("Certificate"),
		OtherFields: kubernetes.UntypedArgs{
			"spec": &pulumi.Map{
				"commonName": pulumi.String("dex.ediri.cloud"),
				"dnsNames": pulumi.StringArray{
					pulumi.String("dex.ediri.cloud"),
				},
				"issuerRef": &pulumi.Map{
					"name": pulumi.String("letsencrypt-staging"),
					"kind": pulumi.String("ClusterIssuer"),
				},
				"secretName": pulumi.String("dex-tls"),
			},
		},
	}, pulumi.Provider(p.provider), pulumi.Parent(dex), pulumi.Parent(p.certManager), pulumi.Parent(p.ingress))
	if err != nil {
		return err
	}
	return nil
}

func (p *ProviderDependency) createCertManager() error {
	certManager, err := helm.NewRelease(p.ctx, "jetstack", &helm.ReleaseArgs{
		Name:            pulumi.String("cert-manager"),
		Chart:           pulumi.String("cert-manager"),
		Version:         pulumi.String("v1.6.1"),
		Namespace:       pulumi.String("cert-manager"),
		CreateNamespace: pulumi.Bool(true),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String("https://charts.jetstack.io"),
		},
		Values: pulumi.Map{
			"prometheus": pulumi.Map{
				"enabled": pulumi.Bool(false),
				"servicemonitor": pulumi.Map{
					"enabled": pulumi.Bool(false),
				},
			},
			"serviceAccount": pulumi.Map{
				"automountServiceAccountToken": pulumi.Bool(true),
			},
			"installCRDs": pulumi.Bool(true),
		},
	}, pulumi.Provider(p.provider))
	if err != nil {
		return err
	}
	p.certManager = certManager

	scw := config.New(p.ctx, "scaleway")
	scaleWayWebHook, err := helm.NewChart(p.ctx, "scaleway-webhook", helm.ChartArgs{
		Path:      pulumi.String("./scaleway-webhook"),
		Chart:     pulumi.String("scaleway-webhook"),
		Namespace: pulumi.String("cert-manager"),
	}, pulumi.Provider(p.provider), pulumi.Parent(certManager))
	if err != nil {
		return err
	}

	webhookSecret, err := v1.NewSecret(p.ctx, "webhook-dns-credentials", &v1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("webhook-dns-credentials"),
			Namespace: pulumi.String("cert-manager"),
		},
		StringData: pulumi.StringMap{
			"access_key": pulumi.String(scw.Get("access_key")),
			"secret_key": pulumi.String(scw.Get("secret_key")),
		},
		Type: pulumi.String("Opaque"),
	}, pulumi.Provider(p.provider), pulumi.Parent(certManager))
	if err != nil {
		return err
	}

	_, err = apiextensions.NewCustomResource(p.ctx, "letsencrypt-staging", &apiextensions.CustomResourceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String("letsencrypt-staging"),
		},
		ApiVersion: pulumi.String("cert-manager.io/v1"),
		Kind:       pulumi.String("ClusterIssuer"),
		OtherFields: kubernetes.UntypedArgs{
			"spec": &pulumi.Map{
				"acme": pulumi.Map{
					"server": pulumi.String("https://acme-v02.api.letsencrypt.org/directory"),
					//"server": pulumi.String("https://acme-staging-v02.api.letsencrypt.org/directory"),
					"email": pulumi.String("info@ediri.de"),
					"privateKeySecretRef": pulumi.StringMap{
						"name": pulumi.String("letsencrypt-staging"),
					},
					"solvers": pulumi.Array{
						pulumi.Map{
							"dns01": pulumi.Map{
								"webhook": pulumi.Map{
									"groupName":  pulumi.String("acme.scaleway.com"),
									"solverName": pulumi.String("scaleway"),
									"config": pulumi.Map{
										"accessKeySecretRef": pulumi.Map{
											"key":  pulumi.String("access_key"),
											"name": pulumi.String("webhook-dns-credentials"),
										},
										"secretKeySecretRef": pulumi.Map{
											"key":  pulumi.String("secret_key"),
											"name": pulumi.String("webhook-dns-credentials"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, pulumi.Provider(p.provider), pulumi.Parent(certManager), pulumi.Parent(webhookSecret), pulumi.Parent(scaleWayWebHook))
	if err != nil {
		return err
	}
	return nil
}

func (p *ProviderDependency) createIngress() error {
	ingress, err := helm.NewRelease(p.ctx, "ingress-nginx", &helm.ReleaseArgs{
		Name:            pulumi.String("ingress-nginx"),
		Chart:           pulumi.String("ingress-nginx"),
		Version:         pulumi.String("4.0.13"),
		Namespace:       pulumi.String("ingress-nginx"),
		CreateNamespace: pulumi.Bool(true),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String("https://kubernetes.github.io/ingress-nginx"),
		},
		Values: pulumi.Map{
			"serviceAccount": pulumi.Map{
				"automountServiceAccountToken": pulumi.Bool(true),
			},
			"controller": pulumi.Map{
				"metrics": pulumi.Map{
					"enabled": pulumi.Bool(false),
					"serviceMonitor": pulumi.Map{
						"enabled": pulumi.Bool(false),
					},
				},
				// CVE-2021-25742-nginx-ingress-snippet-annotation-vulnerability
				// https://www.accurics.com/blog/security-blog/kubernetes-security-preventing-secrets-exfiltration-cve-2021-25742/
				"allowSnippetAnnotations": pulumi.Bool(false),
			},
		},
	}, pulumi.Provider(p.provider))
	if err != nil {
		return err
	}
	p.ingress = ingress
	return nil
}

func (p *ProviderDependency) createExternalDns() error {
	externalDNSNS, err := v1.NewNamespace(p.ctx, "external-dns", &v1.NamespaceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String("external-dns"),
		},
	}, pulumi.Provider(p.provider))
	if err != nil {
		return err
	}

	scw := config.New(p.ctx, "scaleway")

	scalewaySecret, err := v1.NewSecret(p.ctx, "external-dns-credentials", &v1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("external-dns-credentials"),
			Namespace: externalDNSNS.Metadata.Name(),
		},
		StringData: pulumi.StringMap{
			"access_key": pulumi.String(scw.Get("access_key")),
			"secret_key": pulumi.String(scw.Get("secret_key")),
		},
		Type: pulumi.String("Opaque"),
	}, pulumi.Provider(p.provider), pulumi.Parent(externalDNSNS))
	if err != nil {
		return err
	}

	_, err = helm.NewRelease(p.ctx, "external-dns", &helm.ReleaseArgs{
		Name:            pulumi.String("external-dns"),
		Chart:           pulumi.String("external-dns"),
		Version:         pulumi.String("1.7.1"),
		Namespace:       externalDNSNS.Metadata.Name(),
		CreateNamespace: pulumi.Bool(false),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String("https://kubernetes-sigs.github.io/external-dns"),
		},
		Values: pulumi.Map{
			"env": pulumi.Array{
				pulumi.Map{
					"name": pulumi.String("SCW_ACCESS_KEY"),
					"valueFrom": pulumi.Map{
						"secretKeyRef": pulumi.Map{
							"name": scalewaySecret.Metadata.Name(),
							"key":  pulumi.String("access_key"),
						},
					},
				},
				pulumi.Map{
					"name": pulumi.String("SCW_SECRET_KEY"),
					"valueFrom": pulumi.Map{
						"secretKeyRef": pulumi.Map{
							"name": scalewaySecret.Metadata.Name(),
							"key":  pulumi.String("secret_key"),
						},
					},
				},
			},
			"serviceMonitor": pulumi.Map{
				"enabled": pulumi.Bool(false),
				"additionalLabels": pulumi.Map{
					"app": pulumi.String("external-dns"),
				},
			},
			"provider": pulumi.String("scaleway"),
			"domainFilters": pulumi.Array{
				pulumi.String("ediri.cloud"),
			},
			"sources": pulumi.Array{
				pulumi.String("ingress"),
			},
		},
	}, pulumi.Provider(p.provider), pulumi.Parent(externalDNSNS))
	if err != nil {
		return err
	}
	return nil
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cluster, err := scaleway.NewKubernetesCluster(ctx, "pulumi-kapsule", &scaleway.KubernetesClusterArgs{
			Name:    pulumi.String("pulumi-kapsule"),
			Version: pulumi.String("1.23"),
			Region:  pulumi.String("fr-par"),
			Cni:     pulumi.String("cilium"),
			FeatureGates: pulumi.StringArray{
				pulumi.String("HPAScaleToZero"),
			},
			Tags: pulumi.StringArray{
				pulumi.String("pulumi"),
			},
			AutoUpgrade: &scaleway.KubernetesClusterAutoUpgradeArgs{
				Enable:                     pulumi.Bool(true),
				MaintenanceWindowStartHour: pulumi.Int(3),
				MaintenanceWindowDay:       pulumi.String("sunday"),
			},
			AdmissionPlugins: pulumi.StringArray{
				pulumi.String("AlwaysPullImages"),
			},

			OpenIdConnectConfig: &scaleway.KubernetesClusterOpenIdConnectConfigArgs{
				IssuerUrl:      pulumi.String("https://dex.ediri.cloud/dex"),
				ClientId:       pulumi.String("kubernetes"),
				UsernameClaim:  pulumi.String("preferred_username"),
				UsernamePrefix: pulumi.String("oidc:"),
			},
		})
		if err != nil {
			return err
		}
		pool, err := scaleway.NewKubernetesNodePool(ctx, "pulumi-kapsule-pool", &scaleway.KubernetesNodePoolArgs{
			Zone:        pulumi.String("fr-par-1"),
			Name:        pulumi.String("pulumi-kapsule-pool"),
			NodeType:    pulumi.String("DEV1-L"),
			Size:        pulumi.Int(1),
			Autoscaling: pulumi.Bool(true),
			MinSize:     pulumi.Int(1),
			MaxSize:     pulumi.Int(3),
			Autohealing: pulumi.Bool(true),
			ClusterId:   cluster.ID(),
		})
		if err != nil {
			return err
		}
		ctx.Export("cluster_id", cluster.ID())
		ctx.Export("kubeconfig", pulumi.ToSecret(cluster.Kubeconfigs.Index(pulumi.Int(0)).ConfigFile()))

		provider, err := kubernetes.NewProvider(ctx, "kubernetes", &kubernetes.ProviderArgs{
			Kubeconfig: cluster.Kubeconfigs.Index(pulumi.Int(0)).ConfigFile(),
		}, pulumi.Parent(pool))

		dep := &ProviderDependency{
			ctx:      ctx,
			provider: provider,
		}
		if err != nil {
			return err
		}

		_, err = rbac.NewClusterRoleBinding(ctx, "admin-binding", &rbac.ClusterRoleBindingArgs{
			Metadata: metav1.ObjectMetaArgs{
				Name: pulumi.String("admin-binding"),
			},
			Subjects: rbac.SubjectArray{
				rbac.SubjectArgs{
					Kind: pulumi.String("User"),
					Name: pulumi.String("oidc:dirien"),
				},
			},
			RoleRef: rbac.RoleRefArgs{
				Kind: pulumi.String("ClusterRole"),
				Name: pulumi.String("cluster-admin"),
			},
		}, pulumi.Provider(provider))
		if err != nil {
			return err
		}

		if err := dep.createIngress(); err != nil {
			return err
		}

		if err := dep.createCertManager(); err != nil {
			return err
		}

		if err := dep.createExternalDns(); err != nil {
			return err
		}

		if err := dep.createDex(); err != nil {
			return err
		}

		return nil
	})
}
