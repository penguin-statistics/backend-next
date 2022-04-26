package v2

// Dummy is for swagger to find the correct package by using an
// import alias in the used file. For some files, typically
// in the controller, the model is not actually been referenced as
// a type. Therefore, causing the import to be stripped and causing
// swag-go to fail finding the right package.
//
// The intended way to use this is:
//
//     import modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
//     // then, reference the Dummy type to make Go compiler happy as we want to keep our import alias intact
//     var _ modelv2.Dummy
type Dummy struct{}
