package types

type UpyunInitResponse struct {
	// The URL to request for uploading the image
	URL string `json:"url"`

	// The authorization header to be used in the upload request
	Authorization string `json:"authorization"`

	// Policy for the upload request
	Policy string `json:"policy"`
}
