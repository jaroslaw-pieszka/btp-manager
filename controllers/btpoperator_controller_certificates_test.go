package controllers

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/kyma-project/btp-manager/api/v1alpha1"
	"github.com/kyma-project/btp-manager/internal/certs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimachienerytypes "k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type certificationsTimeOpts struct {
	CaCertificateExpiration time.Duration
	WebhookCertExpiration   time.Duration
	ExpirationBoundary      time.Duration
}

var _ = Describe("BTP Operator controller - certificates", Label("certs"), func() {
	var cr *v1alpha1.BtpOperator
	var chartPathForProcess, resourcesPathForProcess string
	var orgCaCertificateExpiration, orgWebhookCertExpiration, orgExpirationBoundary time.Duration
	var certificateDataKeyName = reconciler.buildKeyNameWithExtension(CaSecretDataPrefix, CertificatePostfix)
	var privateKeyDataKeyName = reconciler.buildKeyNameWithExtension(CaSecretDataPrefix, RsaKeyPostfix)
	var webhookCertDataKeyName = reconciler.buildKeyNameWithExtension(WebhookSecretDataPrefix, CertificatePostfix)
	var webhookPrivateKeyDataKeyName = reconciler.buildKeyNameWithExtension(WebhookSecretDataPrefix, RsaKeyPostfix)

	restoreOriginalCertificateTimes := func() {
		CaCertificateExpiration = orgCaCertificateExpiration
		WebhookCertificateExpiration = orgWebhookCertExpiration
		ExpirationBoundary = orgExpirationBoundary
	}

	certBeforeEach := func(opts *certificationsTimeOpts) {
		GinkgoWriter.Println("--- PROCESS:", GinkgoParallelProcess(), "---")
		secret, err := createCorrectSecretFromYaml()
		Expect(err).To(BeNil())
		Expect(k8sClient.Patch(ctx, secret, client.Apply, client.ForceOwnership, client.FieldOwner(operatorName))).To(Succeed())

		orgCaCertificateExpiration = CaCertificateExpiration
		orgWebhookCertExpiration = WebhookCertificateExpiration
		orgExpirationBoundary = ExpirationBoundary

		ChartPath = "../module-chart/chart"
		ResourcesPath = "../module-resources"
		chartPathForProcess = fmt.Sprintf("%s%d", defaultChartPath, GinkgoParallelProcess())
		resourcesPathForProcess = fmt.Sprintf("%s%d", defaultResourcesPath, GinkgoParallelProcess())
		Expect(createChartOrResourcesCopyWithoutWebhooks(ChartPath, chartPathForProcess)).To(Succeed())
		Expect(createChartOrResourcesCopyWithoutWebhooks(ResourcesPath, resourcesPathForProcess)).To(Succeed())
		ChartPath = chartPathForProcess
		ResourcesPath = resourcesPathForProcess

		if opts != nil {
			CaCertificateExpiration = opts.CaCertificateExpiration
			WebhookCertificateExpiration = opts.WebhookCertExpiration
			ExpirationBoundary = opts.ExpirationBoundary
		}

		cr = createDefaultBtpOperator()
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		Eventually(updateCh).Should(Receive(matchState(v1alpha1.StateReady)))
	}

	certAfterEach := func() {
		cr = &v1alpha1.BtpOperator{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: kymaNamespace, Name: btpOperatorName}, cr)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, cr)).Should(Succeed())
		Eventually(updateCh).Should(Receive(matchDeleted()))
		Expect(isCrNotFound()).To(BeTrue())

		deleteSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: kymaNamespace, Name: SecretName}, deleteSecret)).To(Succeed())
		Expect(k8sClient.Delete(ctx, deleteSecret)).To(Succeed())

		restoreOriginalCertificateTimes()

		Expect(os.RemoveAll(chartPathForProcess)).To(Succeed())
		Expect(os.RemoveAll(resourcesPathForProcess)).To(Succeed())

		ChartPath = defaultChartPath
		ResourcesPath = defaultResourcesPath
	}

	ensureReconciliationQueueIsEmpty := func() {
		Eventually(func() int { return reconciler.workqueueSize }).WithTimeout(time.Second * 5).WithPolling(time.Millisecond * 100).Should(Equal(0))
	}

	ensureCorrectState := func() {
		ensureReconciliationQueueIsEmpty()
		ok, err := reconciler.isWebhookSecretCertSignedByCaSecretCert(ctx)
		Expect(err).To(BeNil())
		Expect(ok).To(BeTrue())
		ensureAllWebhooksManagedByBtpOperatorHaveCorrectCABundles()
	}

	Describe("certs created with default expiration times", func() {
		BeforeEach(func() {
			certBeforeEach(nil)
		})

		AfterEach(func() {
			certAfterEach()
		})

		When("certs don't exist in the cluster prior to provisioning", func() {
			It("should generate correct certs pair", func() {
				ensureCorrectState()
			})
		})

		When("CA certificate changes", func() {
			It("should fully regenerate CA certificate and webhook certificate", func() {
				testGeneratedCaCert, testGeneratedPrivateKey, err := certs.GenerateSelfSignedCertificate(time.Now().Add(CaCertificateExpiration))
				testGeneratedPrivateKeyArray, err := structToByteArray(testGeneratedPrivateKey)
				Expect(err).To(BeNil())

				caSecret := getSecret(CaSecretName)
				caCertificateOriginal, ok := caSecret.Data[certificateDataKeyName]
				Expect(ok).To(BeTrue())

				caPrivateKeyOriginal, ok := caSecret.Data[privateKeyDataKeyName]
				Expect(ok).To(BeTrue())

				initialWebhookSecret := getSecret(WebhookSecret)
				initialWebhookCert, ok := initialWebhookSecret.Data[webhookCertDataKeyName]
				Expect(ok).To(BeTrue())

				// this forces full regeneration by change of CA certificate
				replaceSecretData(caSecret, certificateDataKeyName, testGeneratedCaCert, privateKeyDataKeyName, testGeneratedPrivateKeyArray)

				GinkgoWriter.Println("Secret overwritten: ", time.Now().Format(time.RFC3339Nano))

				// updated CA certificate should be the result of full regeneration so it is different from initial one and test generated one
				Eventually(func() bool {
					currentSecret := getSecret(CaSecretName)
					currentCaCert, ok := currentSecret.Data[certificateDataKeyName]
					isRegeneratedCA := ok && !bytes.Equal(currentCaCert, testGeneratedCaCert) && !bytes.Equal(currentCaCert, caCertificateOriginal)

					currentPrivateKey, ok := currentSecret.Data[privateKeyDataKeyName]
					isRegeneratedPrivateKey := ok && !bytes.Equal(currentPrivateKey, testGeneratedPrivateKey) && !bytes.Equal(currentPrivateKey, caPrivateKeyOriginal)

					currentWebhookSecret := getSecret(WebhookSecret)
					currentWebhookCert, ok := currentWebhookSecret.Data[webhookCertDataKeyName]
					isRegeneratedWebhookCert := ok && !bytes.Equal(currentWebhookCert, initialWebhookCert)

					return isRegeneratedCA && isRegeneratedPrivateKey && isRegeneratedWebhookCert

				}).WithTimeout(time.Second * 5).WithPolling(time.Second).Should(Equal(true))

				ensureCorrectState()
			})
		})

		When("webhook certificate changes and is signed by same CA certificate", func() {
			It("CA certificate is not changed, webhook certificate is regenerated", func() {
				beforeCaSecret := getSecret(CaSecretName)

				currentCa, err := reconciler.getDataFromSecret(ctx, CaSecretName)
				Expect(err).To(BeNil())
				ca, err := reconciler.getValueByKey(reconciler.buildKeyNameWithExtension(CaSecretDataPrefix, CertificatePostfix), currentCa)
				Expect(err).To(BeNil())
				pk, err := reconciler.getValueByKey(reconciler.buildKeyNameWithExtension(CaSecretDataPrefix, RsaKeyPostfix), currentCa)
				Expect(err).To(BeNil())
				currentWebhookSecret := getSecret(WebhookSecret)
				originalWebhookSecret := currentWebhookSecret

				newWebhookCertificate, newWebhookPrivateKey, err := certs.GenerateSignedCertificate(time.Now().Add(WebhookCertificateExpiration), ca, pk)
				Expect(err).To(BeNil())
				newWebhookPrivateKeyStructured, err := structToByteArray(newWebhookPrivateKey)
				Expect(err).To(BeNil())

				webhookCert := getSecret(WebhookSecret)
				replaceSecretData(webhookCert, reconciler.buildKeyNameWithExtension(WebhookSecretDataPrefix, CertificatePostfix), newWebhookCertificate, reconciler.buildKeyNameWithExtension(WebhookSecretDataPrefix, RsaKeyPostfix), newWebhookPrivateKeyStructured)
				ensureReconciliationQueueIsEmpty()

				originalWebhookCert, ok := originalWebhookSecret.Data[reconciler.buildKeyNameWithExtension(WebhookSecretDataPrefix, CertificatePostfix)]
				Expect(!bytes.Equal(originalWebhookCert, newWebhookCertificate))

				currentWebhookSecret = getSecret(WebhookSecret)
				currentWebhookCert, ok := currentWebhookSecret.Data[reconciler.buildKeyNameWithExtension(WebhookSecretDataPrefix, CertificatePostfix)]
				Expect(ok).To(BeTrue())
				Expect(bytes.Equal(currentWebhookCert, newWebhookCertificate))

				afterCaSecret := getSecret(CaSecretName)
				afterCaSecretCert, ok := afterCaSecret.Data[reconciler.buildKeyNameWithExtension(CaSecretDataPrefix, CertificatePostfix)]
				beforeCaSecretCert, ok := beforeCaSecret.Data[reconciler.buildKeyNameWithExtension(CaSecretDataPrefix, CertificatePostfix)]
				Expect(bytes.Equal(afterCaSecretCert, beforeCaSecretCert))
				ensureCorrectState()
			})
		})

		When("webhook certificate is signed by different CA certificate", func() { //CAVEAT this fails occasionally
			It("CA certificate and webhook certificate are fully regenerated", func() {

				testGeneratedCaCertificate, testGeneratedPrivateKey, err := certs.GenerateSelfSignedCertificate(time.Now().Add(CaCertificateExpiration))
				Expect(err).To(BeNil())

				testGeneratedWebhookCert, newWebhookPrivateKey, err := certs.GenerateSignedCertificate(time.Now().Add(WebhookCertificateExpiration), testGeneratedCaCertificate, testGeneratedPrivateKey)
				testGeneratedPrivateKeyArray, err := structToByteArray(newWebhookPrivateKey)
				Expect(err).To(BeNil())

				initialCaSecret := getSecret(CaSecretName)
				initialCaCert, ok := initialCaSecret.Data[certificateDataKeyName]
				Expect(ok).To(BeTrue())

				initialWebhookSecret := getSecret(WebhookSecret)
				initialWebhhookCert, ok := initialWebhookSecret.Data[webhookCertDataKeyName]
				Expect(ok).To(BeTrue())

				webhookCertSecret := initialWebhookSecret
				// this forces full regeneration since this webhook certificate is signed by different CA certificate (test generated)
				replaceSecretData(webhookCertSecret, webhookCertDataKeyName, testGeneratedWebhookCert, webhookPrivateKeyDataKeyName, testGeneratedPrivateKeyArray)

				GinkgoWriter.Println("Secret overwritten: ", time.Now().Format(time.RFC3339Nano))

				// updated CA certificate should be the result of full regeneration so it is different from old one and new one
				// accordingly webhook certificate should be different from old one and new one

				Eventually(func() bool {
					currentCaSecret := getSecret(CaSecretName)
					currentCaCert, ok := currentCaSecret.Data[certificateDataKeyName]
					isRegeneratedCA := ok && !bytes.Equal(currentCaCert, testGeneratedCaCertificate) && !bytes.Equal(currentCaCert, initialCaCert)

					currentWebhookSecret := getSecret(WebhookSecret)
					currentWebhookCert, ok := currentWebhookSecret.Data[webhookCertDataKeyName]
					isRegeneratedWebhookCert := ok && !bytes.Equal(currentWebhookCert, testGeneratedWebhookCert) && !bytes.Equal(currentWebhookCert, initialWebhhookCert)

					return isRegeneratedCA && isRegeneratedWebhookCert

				}).WithTimeout(time.Second * 5).WithPolling(time.Second).Should(Equal(true))

				ensureCorrectState()
			})
		})

		When("webhook caBundle modified with new CA certificate", func() {
			It("should be reconciled to existing CA certificate", func() {
				newCaCertificate, _, err := certs.GenerateSelfSignedCertificate(time.Now().Add(CaCertificateExpiration))
				Expect(err).To(BeNil())
				updated := replaceCaBundleInMutatingWebhooks(newCaCertificate)
				if !updated {
					updated = replaceCaBundleInValidatingWebhooks(newCaCertificate)
				}
				Expect(updated).To(BeTrue())
				ensureCorrectState()
			})
		})

		When("webhook caBundle modified with some dummy text", func() {
			It("should be reconciled to existing CA certificate", func() {
				dummy := []byte("dummy")
				updated := replaceCaBundleInMutatingWebhooks(dummy)
				if !updated {
					updated = replaceCaBundleInValidatingWebhooks(dummy)
				}
				Expect(updated).To(BeTrue())
				ensureCorrectState()
			})
		})
	})

	Describe("certs created with custom expiration times", func() {
		fakeSeconds := 30.0
		fakeExpiration := 10.0

		AfterEach(func() {
			certAfterEach()
		})

		When("webhook certificate expires", func() {
			BeforeEach(func() {
				timeOpts := &certificationsTimeOpts{
					CaCertificateExpiration: CaCertificateExpiration,
					WebhookCertExpiration:   time.Second * time.Duration(fakeSeconds),
					ExpirationBoundary:      time.Second * time.Duration(fakeExpiration),
				}
				certBeforeEach(timeOpts)
			})

			It("CA certificate is not changed, webhook certificate is regenerated", func() {
				caSecretBeforeExpiration := getSecret(CaSecretName)
				webhookSecretBeforeExpiration := getSecret(WebhookSecret)
				Expect(checkHowManySecondsToExpiration(WebhookSecret)).Should(BeNumerically("<=", fakeSeconds))

				restoreOriginalCertificateTimes()
				ensureReconciliationQueueIsEmpty()
				_, err := reconciler.Reconcile(ctx, controllerruntime.Request{NamespacedName: apimachienerytypes.NamespacedName{
					Namespace: cr.Namespace,
					Name:      cr.Name,
				}})
				Expect(err).To(BeNil())
				ensureReconciliationQueueIsEmpty()
				caSecretAfterExpiration := getSecret(CaSecretName)
				webhookSecretAfterExpiration := getSecret(WebhookSecret)
				Expect(reflect.DeepEqual(caSecretBeforeExpiration.Data, caSecretAfterExpiration.Data)).To(BeTrue())
				Expect(reflect.DeepEqual(webhookSecretBeforeExpiration.Data, webhookSecretAfterExpiration.Data)).To(BeFalse())
				Expect(checkHowManySecondsToExpiration(WebhookSecret)).Should(BeNumerically(">=", fakeSeconds))

				ensureCorrectState()
			})
		})

		When("CA certificate expires", func() {
			BeforeEach(func() {
				timeOpts := &certificationsTimeOpts{
					CaCertificateExpiration: time.Second * time.Duration(fakeSeconds),
					WebhookCertExpiration:   orgWebhookCertExpiration,
					ExpirationBoundary:      time.Second * time.Duration(fakeExpiration),
				}
				certBeforeEach(timeOpts)
			})

			It("fully regenerate of CA certificate and webhook certificate", func() {
				caSecretBeforeExpiration := getSecret(CaSecretName)
				webhookSecretBeforeExpiration := getSecret(WebhookSecret)
				Expect(checkHowManySecondsToExpiration(CaSecretName)).Should(BeNumerically("<=", fakeSeconds))
				restoreOriginalCertificateTimes()
				ensureReconciliationQueueIsEmpty()
				_, err := reconciler.Reconcile(ctx, controllerruntime.Request{NamespacedName: apimachienerytypes.NamespacedName{
					Namespace: cr.Namespace,
					Name:      cr.Name,
				}})
				Expect(err).To(BeNil())
				ensureReconciliationQueueIsEmpty()
				caSecretAfterExpiration := getSecret(CaSecretName)
				webhookSecretAfterExpiration := getSecret(WebhookSecret)
				Expect(reflect.DeepEqual(caSecretBeforeExpiration.Data, caSecretAfterExpiration.Data)).To(BeFalse())
				Expect(reflect.DeepEqual(webhookSecretBeforeExpiration.Data, webhookSecretAfterExpiration.Data)).To(BeFalse())
				Expect(checkHowManySecondsToExpiration(WebhookSecret)).Should(BeNumerically(">=", fakeSeconds))
				Expect(checkHowManySecondsToExpiration(CaSecretName)).Should(BeNumerically(">=", fakeSeconds))
				ensureCorrectState()
			})
		})
	})
})
