package sparkoperatormodule

const (
	SparkOperatorComponentName = "spark-operator"
	platformConfigMapName      = "odh-sparkoperator-config"
	platformVersionKey         = "platformVersion"
	platformReleaseName        = "platform"

	SparkOperatorManifestSourcePathODH   = "config/overlays/odh"
	SparkOperatorManifestSourcePathRHOAI = "config/overlays/rhoai"

	sparkOperatorControllerDeployment = "spark-operator-controller"
	sparkOperatorWebhookDeployment    = "spark-operator-webhook"

	fieldOwner = "spark-operator-module-controller"

	ConditionSparkOperatorReady = "SparkOperatorReady"
)
