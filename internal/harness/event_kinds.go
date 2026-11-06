package harness

// Event kinds used at real send sites. Keep the frontend policy fixture current.
const (
	EventAttachmentOmitted        = "attachment_omitted"
	EventChannelDeliveryError     = "channel_delivery_error"
	EventChannelError             = "channel_error"
	EventCollaboration            = "collaboration"
	EventCommandChanged           = "command_changed"
	EventConfirmation             = "confirmation"
	EventContextCompacted         = "context_compacted"
	EventContextCompacting        = "context_compacting"
	EventContextCompactionFailed  = "context_compaction_failed"
	EventContextRetry             = "context_retry"
	EventContextUsage             = "context_usage"
	EventControlConfirmation      = "control_confirmation"
	EventExtensionActivity        = "extension_activity"
	EventExtensionError           = "extension_error"
	EventHeartbeat                = "heartbeat"
	EventInputAdopted             = "input_adopted"
	EventInputChanged             = "input_changed"
	EventMemorySources            = "memory_sources"
	EventModelFinished            = "model_finished"
	EventModelInterrupted         = "model_interrupted"
	EventModelRetryWait           = "model_retry_wait"
	EventModelSelected            = "model_selected"
	EventModelStarted             = "model_started"
	EventModelText                = "model_text"
	EventReconnect                = "reconnect"
	EventRefresh                  = "refresh"
	EventRequestError             = "request_error"
	EventRunCompleted             = "run_completed"
	EventRunSettled               = "run_settled"
	EventRunStarted               = "run_started"
	EventTelegramAccessStopFailed = "telegram_access_stop_failed"
	EventToolFinished             = "tool_finished"
	EventToolStarted              = "tool_started"
	EventToolVerification         = "tool_verification"
	EventUnresolved               = "unresolved"
)

func EventKinds() []string {
	return []string{EventAttachmentOmitted, EventChannelDeliveryError, EventChannelError, EventCollaboration, EventCommandChanged, EventConfirmation, EventContextCompacted, EventContextCompacting, EventContextCompactionFailed, EventContextRetry, EventContextUsage, EventControlConfirmation, EventExtensionActivity, EventExtensionError, EventHeartbeat, EventInputAdopted, EventInputChanged, EventMemorySources, EventModelFinished, EventModelInterrupted, EventModelRetryWait, EventModelSelected, EventModelStarted, EventModelText, EventReconnect, EventRefresh, EventRequestError, EventRunCompleted, EventRunSettled, EventRunStarted, EventTelegramAccessStopFailed, EventToolFinished, EventToolStarted, EventToolVerification, EventUnresolved}
}
