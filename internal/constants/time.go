package constants

const (
	FakeEndTimeMilli int64 = 62141368179000

	CNServerStartTimeMilli int64 = 1556676000000
	USServerStartTimeMilli int64 = 1579190400000
	JPServerStartTimeMilli int64 = 1579140000000
	KRServerStartTimeMilli int64 = 1579140000000
)

var ServerStartTimeMapMilli = map[string]int64{
	"CN": CNServerStartTimeMilli,
	"US": USServerStartTimeMilli,
	"JP": JPServerStartTimeMilli,
	"KR": KRServerStartTimeMilli,
}
