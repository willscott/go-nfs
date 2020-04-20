package nfs

import (
	"encoding"
	"encoding/binary"
	"fmt"
)

// RPCError provides the error interface for errors thrown by
// procedures to be transmitted over the XDR RPC channel
type RPCError interface {
	// An RPCError is an `error` with this method
	Error() string
	// Code is the RPC Response code to send
	Code() ResponseCode
	// BinaryMarshaler is the on-wire representation of this error
	encoding.BinaryMarshaler
}

// AuthStat is an enumeration of why authentication ahs failed
type AuthStat uint32

// AuthStat Codes
const (
	AuthStatOK AuthStat = iota
	AuthStatBadCred
	AuthStatRejectedCred
	AuthStatBadVerifier
	AuthStatRejectedVerfier
	AuthStatTooWeak
	AuthStatInvalidResponse
	AuthStatFailed
	AuthStatKerbGeneric
	AuthStatTimeExpire
	AuthStatTktFile
	AuthStatDecode
	AuthStatNetAddr
	AuthStatRPCGSSCredProblem
	AuthStatRPCGSSCTXProblem
)

// AuthError is an RPCError
type AuthError struct {
	AuthStat
}

// Code for AuthErrors is ResponseCodeAuthError
func (a *AuthError) Code() ResponseCode {
	return ResponseCodeAuthError
}

// Error is a textual representaiton of the auth error. From the RFC
func (a *AuthError) Error() string {
	switch a.AuthStat {
	case AuthStatOK:
		return "Auth Status: OK"
	case AuthStatBadCred:
		return "Auth Status: bad credential"
	case AuthStatRejectedCred:
		return "Auth Status: client must begin new session"
	case AuthStatBadVerifier:
		return "Auth Status: bad verifier"
	case AuthStatRejectedVerfier:
		return "Auth Status: verifier expired or replayed"
	case AuthStatTooWeak:
		return "Auth Status: rejected for security reasons"
	case AuthStatInvalidResponse:
		return "Auth Status: bogus response verifier"
	case AuthStatFailed:
		return "Auth Status: reason unknown"
	case AuthStatKerbGeneric:
		return "Auth Status: kerberos generic error"
	case AuthStatTimeExpire:
		return "Auth Status: time of credential expired"
	case AuthStatTktFile:
		return "Auth Status: problem with ticket file"
	case AuthStatDecode:
		return "Auth Status: can't decode authenticator"
	case AuthStatNetAddr:
		return "Auth Status: wrong net address in ticket"
	case AuthStatRPCGSSCredProblem:
		return "Auth Status: no credentials for user"
	case AuthStatRPCGSSCTXProblem:
		return "Auth Status: problem with context"
	}
	return "Auth Status: Unknown"
}

// MarshalBinary sends the specific auth status
func (a *AuthError) MarshalBinary() (data []byte, err error) {
	var resp [4]byte
	binary.LittleEndian.PutUint32(resp[:], uint32(a.AuthStat))
	return resp[:], nil
}

// RPCMismatchError is an RPCError
type RPCMismatchError struct {
	Low  uint32
	High uint32
}

// Code for RPCMismatchError is ResponseCodeRPCMismatch
func (r *RPCMismatchError) Code() ResponseCode {
	return ResponseCodeRPCMismatch
}

func (r *RPCMismatchError) Error() string {
	return fmt.Sprintf("RPC Mismatch: Expected version between %d and %d.", r.Low, r.High)
}

// MarshalBinary sends the specific rpc mismatch range
func (r *RPCMismatchError) MarshalBinary() (data []byte, err error) {
	var resp [8]byte
	binary.LittleEndian.PutUint32(resp[0:4], uint32(r.Low))
	binary.LittleEndian.PutUint32(resp[4:8], uint32(r.High))
	return resp[:], nil
}
