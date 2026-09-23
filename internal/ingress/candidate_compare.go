package ingress

import (
	"reflect"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// candidateDeploymentMatches compares an immutable candidate after applying
// only defaults Kubernetes is expected to add to a Deployment. Fields with
// application meaning remain exact, including selectors, labels, annotations,
// containers, probes, environment, security, and replica settings.
func candidateDeploymentMatches(existing, desired *appsv1.Deployment) bool {
	if existing == nil || desired == nil || existing.Spec.Selector == nil || desired.Spec.Selector == nil {
		return false
	}
	a := existing.DeepCopy()
	b := desired.DeepCopy()
	normalizeCandidateDeployment(a)
	normalizeCandidateDeployment(b)
	return reflect.DeepEqual(a.Name, b.Name) &&
		reflect.DeepEqual(a.Namespace, b.Namespace) &&
		reflect.DeepEqual(a.Labels, b.Labels) &&
		reflect.DeepEqual(a.Annotations, b.Annotations) &&
		apiequality.Semantic.DeepEqual(a.Spec, b.Spec)
}

func normalizeCandidateDeployment(d *appsv1.Deployment) {
	if d.Spec.Replicas == nil {
		d.Spec.Replicas = int32Ptr(1)
	}
	if d.Spec.RevisionHistoryLimit == nil {
		d.Spec.RevisionHistoryLimit = int32Ptr(10)
	}
	if d.Spec.ProgressDeadlineSeconds == nil {
		d.Spec.ProgressDeadlineSeconds = int32Ptr(600)
	}
	if d.Spec.Strategy.Type == "" {
		d.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	}
	if d.Spec.Strategy.Type == appsv1.RollingUpdateDeploymentStrategyType && d.Spec.Strategy.RollingUpdate == nil {
		unavailable := intstr.FromString("25%")
		surge := intstr.FromString("25%")
		d.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &unavailable,
			MaxSurge:       &surge,
		}
	}
	normalizeCandidatePodSpec(&d.Spec.Template.Spec)
	if d.Annotations != nil {
		delete(d.Annotations, "deployment.kubernetes.io/revision")
		if len(d.Annotations) == 0 {
			d.Annotations = nil
		}
	}
}

func normalizeCandidatePodSpec(p *corev1.PodSpec) {
	if p.RestartPolicy == "" {
		p.RestartPolicy = corev1.RestartPolicyAlways
	}
	if p.DNSPolicy == "" {
		p.DNSPolicy = corev1.DNSClusterFirst
	}
	if p.SchedulerName == "" {
		p.SchedulerName = "default-scheduler"
	}
	if p.TerminationGracePeriodSeconds == nil {
		p.TerminationGracePeriodSeconds = int64Ptr(30)
	}
	if p.EnableServiceLinks == nil {
		p.EnableServiceLinks = boolPtr(true)
	}
	for i := range p.Containers {
		c := &p.Containers[i]
		if c.ImagePullPolicy == "" {
			c.ImagePullPolicy = defaultImagePullPolicy(c.Image)
		}
		if c.TerminationMessagePath == "" {
			c.TerminationMessagePath = "/dev/termination-log"
		}
		if c.TerminationMessagePolicy == "" {
			c.TerminationMessagePolicy = corev1.TerminationMessageReadFile
		}
		for i := range c.Ports {
			if c.Ports[i].Protocol == "" {
				c.Ports[i].Protocol = corev1.ProtocolTCP
			}
		}
		if c.ReadinessProbe != nil {
			normalizeCandidateProbe(c.ReadinessProbe)
		}
		if c.LivenessProbe != nil {
			normalizeCandidateProbe(c.LivenessProbe)
		}
		if c.StartupProbe != nil {
			normalizeCandidateProbe(c.StartupProbe)
		}
	}
}

func normalizeCandidateProbe(p *corev1.Probe) {
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 1
	}
	if p.PeriodSeconds == 0 {
		p.PeriodSeconds = 10
	}
	if p.SuccessThreshold == 0 {
		p.SuccessThreshold = 1
	}
	if p.FailureThreshold == 0 {
		p.FailureThreshold = 3
	}
	if p.HTTPGet != nil && p.HTTPGet.Scheme == "" {
		p.HTTPGet.Scheme = corev1.URISchemeHTTP
	}
}

func defaultImagePullPolicy(image string) corev1.PullPolicy {
	last := image
	digest := false
	if at := strings.LastIndexByte(last, '@'); at >= 0 {
		digest = true
		last = last[:at]
	}
	tag := ""
	if colon := strings.LastIndexByte(last, ':'); colon >= 0 && !strings.Contains(last[colon+1:], "/") {
		tag = last[colon+1:]
	}
	if (!digest && tag == "") || tag == "latest" {
		return corev1.PullAlways
	}
	return corev1.PullIfNotPresent
}

// candidateNetworkPolicyMatches applies only protocol defaults. Policy
// selectors, labels, rule order, ports, and peers remain exact.
func candidateNetworkPolicyMatches(existing, desired *networkingv1.NetworkPolicy) bool {
	if existing == nil || desired == nil {
		return false
	}
	a := existing.DeepCopy()
	b := desired.DeepCopy()
	normalizeCandidatePolicy(a)
	normalizeCandidatePolicy(b)
	return reflect.DeepEqual(a.Name, b.Name) &&
		reflect.DeepEqual(a.Namespace, b.Namespace) &&
		reflect.DeepEqual(a.Labels, b.Labels) &&
		reflect.DeepEqual(a.Annotations, b.Annotations) &&
		apiequality.Semantic.DeepEqual(a.Spec, b.Spec)
}

func normalizeCandidatePolicy(p *networkingv1.NetworkPolicy) {
	normalizePolicyPorts := func(ports []networkingv1.NetworkPolicyPort) {
		for i := range ports {
			if ports[i].Protocol == nil {
				protocol := corev1.ProtocolTCP
				ports[i].Protocol = &protocol
			}
		}
	}
	for i := range p.Spec.Ingress {
		normalizePolicyPorts(p.Spec.Ingress[i].Ports)
	}
	for i := range p.Spec.Egress {
		normalizePolicyPorts(p.Spec.Egress[i].Ports)
	}
	if len(p.Spec.PolicyTypes) == 0 {
		p.Spec.PolicyTypes = append(p.Spec.PolicyTypes, networkingv1.PolicyTypeIngress)
		if len(p.Spec.Egress) > 0 {
			p.Spec.PolicyTypes = append(p.Spec.PolicyTypes, networkingv1.PolicyTypeEgress)
		}
	}
}

func int32Ptr(v int32) *int32 { return &v }
func int64Ptr(v int64) *int64 { return &v }
