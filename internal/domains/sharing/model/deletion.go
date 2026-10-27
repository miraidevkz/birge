package model

// TimerDeleteObjects deletes the photo objects of a deleted member from
// object storage after the database rows are gone (subject = member ID,
// payload DeleteObjectsPayload). Object I/O is a side effect outside the
// deletion transaction, so it goes through a timer created in that
// transaction and retries until every object is gone.
const TimerDeleteObjects = "sharing.DeleteObjects"

// DeleteObjectsPayload lists the object keys to delete. Keys name uploads
// and listings by ID only (UploadObjectKey, ProcessedObjectKey).
type DeleteObjectsPayload struct {
	Keys []string `json:"keys"`
}
