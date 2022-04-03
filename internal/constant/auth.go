package constant

const (
	// PenguinIDAuthMaxCookieAge in seconds
	PenguinIDAuthMaxCookieAgeSec = 313560000

	PenguinIDCookieKey = "userID"

	// PenguinIDSetHeader is for the header in which sets PenguinID
	PenguinIDSetHeader = "X-Penguin-Set-PenguinID"

	// PenguinIDAuthorizationRealm is the authorization realm (prefix of value
	// in the `Authorization` header)
	PenguinIDAuthorizationRealm = "PenguinID"
)
