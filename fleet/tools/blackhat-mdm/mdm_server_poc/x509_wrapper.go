package main

import (
	"bytes"
	"crypto"
	"encoding/asn1"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	// Explicitly import these for their crypto.RegisterHash init side-effects.
	// Keep these as blank imports, even if they're imported above.

	"crypto/dsa" //lint:ignore required for crypto.RegisterHash
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"

	"golang.org/x/crypto/cryptobyte"
	cryptobyte_asn1 "golang.org/x/crypto/cryptobyte/asn1"
)

// ParseCertificateRequest parses a single certificate request from the
// given ASN.1 DER data.
func ParseCertificateRequest2(asn1Data []byte) (*x509.CertificateRequest, error) {
	var csr certificateRequest

	rest, err := asn1.Unmarshal(asn1Data, &csr)
	if err != nil {
		return nil, err
	} else if len(rest) != 0 {
		return nil, asn1.SyntaxError{Msg: "trailing data"}
	}

	return parseCertificateRequest(&csr)
}

// isPrintable reports whether the given b is in the ASN.1 PrintableString set.
// If asterisk is allowAsterisk then '*' is also allowed, reflecting existing
// practice. If ampersand is allowAmpersand then '&' is allowed as well.
func isPrintable(b byte, asterisk asteriskFlag, ampersand ampersandFlag) bool {
	return 'a' <= b && b <= 'z' ||
		'A' <= b && b <= 'Z' ||
		'0' <= b && b <= '9' ||
		'\'' <= b && b <= ')' ||
		'+' <= b && b <= '/' ||
		b == ' ' ||
		b == ':' ||
		b == '=' ||
		b == '?' ||
		b == '!' || // Windows MDM Certificate Parsing Patch
		b == 0 || // Windows MDM Certificate Parsing Patch
		// This is technically not allowed in a PrintableString.
		// However, x509 certificates with wildcard strings don't
		// always use the correct string type so we permit it.
		(bool(asterisk) && b == '*') ||
		// This is not technically allowed either. However, not
		// only is it relatively common, but there are also a
		// handful of CA certificates that contain it. At least
		// one of which will not expire until 2027.
		(bool(ampersand) && b == '&')
}

// oidExtensionRequest is a PKCS #9 OBJECT IDENTIFIER that indicates requested
// extensions in a CSR.
var oidExtensionRequest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 14}

type asteriskFlag bool
type ampersandFlag bool

const (
	allowAsterisk  asteriskFlag = true
	rejectAsterisk asteriskFlag = false

	allowAmpersand  ampersandFlag = true
	rejectAmpersand ampersandFlag = false
)

const (
	nameTypeEmail = 1
	nameTypeDNS   = 2
	nameTypeURI   = 6
	nameTypeIP    = 7
)

var (
	oidNamedCurveP224 = asn1.ObjectIdentifier{1, 3, 132, 0, 33}
	oidNamedCurveP256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 3, 1, 7}
	oidNamedCurveP384 = asn1.ObjectIdentifier{1, 3, 132, 0, 34}
	oidNamedCurveP521 = asn1.ObjectIdentifier{1, 3, 132, 0, 35}
)

var (
	oidExtensionSubjectAltName = []int{2, 5, 29, 17}
)

var (
	oidPublicKeyRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidPublicKeyDSA     = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 1}
	oidPublicKeyECDSA   = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidPublicKeyEd25519 = oidSignatureEd25519
)

var (
	oidSignatureMD2WithRSA      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 2}
	oidSignatureMD5WithRSA      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 4}
	oidSignatureSHA1WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidSignatureSHA256WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSignatureSHA384WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	oidSignatureSHA512WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
	oidSignatureRSAPSS          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidSignatureDSAWithSHA1     = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}
	oidSignatureDSAWithSHA256   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 2}
	oidSignatureECDSAWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}
	oidSignatureECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidSignatureECDSAWithSHA384 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidSignatureECDSAWithSHA512 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	oidSignatureEd25519         = asn1.ObjectIdentifier{1, 3, 101, 112}

	oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}

	oidMGF1 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}

	// oidISOSignatureSHA1WithRSA means the same as oidSignatureSHA1WithRSA
	// but it's specified by ISO. Microsoft's makecert.exe has been known
	// to produce certificates with this OID.
	oidISOSignatureSHA1WithRSA = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 29}
)

var signatureAlgorithmDetails = []struct {
	algo       x509.SignatureAlgorithm
	name       string
	oid        asn1.ObjectIdentifier
	pubKeyAlgo x509.PublicKeyAlgorithm
	hash       crypto.Hash
}{
	{x509.MD2WithRSA, "MD2-RSA", oidSignatureMD2WithRSA, x509.RSA, crypto.Hash(0) /* no value for MD2 */},
	{x509.MD5WithRSA, "MD5-RSA", oidSignatureMD5WithRSA, x509.RSA, crypto.MD5},
	{x509.SHA1WithRSA, "SHA1-RSA", oidSignatureSHA1WithRSA, x509.RSA, crypto.SHA1},
	{x509.SHA1WithRSA, "SHA1-RSA", oidISOSignatureSHA1WithRSA, x509.RSA, crypto.SHA1},
	{x509.SHA256WithRSA, "SHA256-RSA", oidSignatureSHA256WithRSA, x509.RSA, crypto.SHA256},
	{x509.SHA384WithRSA, "SHA384-RSA", oidSignatureSHA384WithRSA, x509.RSA, crypto.SHA384},
	{x509.SHA512WithRSA, "SHA512-RSA", oidSignatureSHA512WithRSA, x509.RSA, crypto.SHA512},
	{x509.SHA256WithRSAPSS, "SHA256-RSAPSS", oidSignatureRSAPSS, x509.RSA, crypto.SHA256},
	{x509.SHA384WithRSAPSS, "SHA384-RSAPSS", oidSignatureRSAPSS, x509.RSA, crypto.SHA384},
	{x509.SHA512WithRSAPSS, "SHA512-RSAPSS", oidSignatureRSAPSS, x509.RSA, crypto.SHA512},
	{x509.DSAWithSHA1, "DSA-SHA1", oidSignatureDSAWithSHA1, x509.DSA, crypto.SHA1},
	{x509.DSAWithSHA256, "DSA-SHA256", oidSignatureDSAWithSHA256, x509.DSA, crypto.SHA256},
	{x509.ECDSAWithSHA1, "ECDSA-SHA1", oidSignatureECDSAWithSHA1, x509.ECDSA, crypto.SHA1},
	{x509.ECDSAWithSHA256, "ECDSA-SHA256", oidSignatureECDSAWithSHA256, x509.ECDSA, crypto.SHA256},
	{x509.ECDSAWithSHA384, "ECDSA-SHA384", oidSignatureECDSAWithSHA384, x509.ECDSA, crypto.SHA384},
	{x509.ECDSAWithSHA512, "ECDSA-SHA512", oidSignatureECDSAWithSHA512, x509.ECDSA, crypto.SHA512},
	{x509.PureEd25519, "Ed25519", oidSignatureEd25519, x509.Ed25519, crypto.Hash(0) /* no pre-hashing */},
}

var (
	bitStringType        = reflect.TypeOf(asn1.BitString{})
	objectIdentifierType = reflect.TypeOf(asn1.ObjectIdentifier{})
	enumeratedType       = reflect.TypeOf(asn1.Enumerated(0))
	flagType             = reflect.TypeOf(asn1.Flag(false))
	timeType             = reflect.TypeOf(time.Time{})
	rawValueType         = reflect.TypeOf(asn1.RawValue{})
	rawContentsType      = reflect.TypeOf(asn1.RawContent(nil))
	bigIntType           = reflect.TypeOf(new(big.Int))
)

// ASN.1 class types represent the namespace of the tag.
const (
	classUniversal       = 0
	classApplication     = 1
	classContextSpecific = 2
	classPrivate         = 3
)

type tagAndLength struct {
	class, tag, length int
	isCompound         bool
}

// pkcs1PublicKey reflects the ASN.1 structure of a PKCS #1 public key.
type pkcs1PublicKey struct {
	n *big.Int
	e int
}

type publicKeyInfo struct {
	Raw       asn1.RawContent
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

type tbsCertificateRequest struct {
	Raw           asn1.RawContent
	Version       int
	Subject       asn1.RawValue
	PublicKey     publicKeyInfo
	RawAttributes []asn1.RawValue `asn1:"tag:0"`
}

type certificateRequest struct {
	Raw                asn1.RawContent
	TBSCSR             tbsCertificateRequest
	SignatureAlgorithm pkix.AlgorithmIdentifier
	SignatureValue     asn1.BitString
}

// pssParameters reflects the parameters in an AlgorithmIdentifier that
// specifies RSA PSS. See RFC 3447, Appendix A.2.3.
type pssParameters struct {
	// The following three fields are not marked as
	// optional because the default values specify SHA-1,
	// which is no longer suitable for use in signatures.
	Hash         pkix.AlgorithmIdentifier `asn1:"explicit,tag:0"`
	MGF          pkix.AlgorithmIdentifier `asn1:"explicit,tag:1"`
	SaltLength   int                      `asn1:"explicit,tag:2"`
	TrailerField int                      `asn1:"optional,explicit,tag:3,default:1"`
}

// fieldParameters is the parsed representation of tag string from a structure field.
type fieldParameters struct {
	optional     bool   // true iff the field is OPTIONAL
	explicit     bool   // true iff an EXPLICIT tag is in use.
	application  bool   // true iff an APPLICATION tag is in use.
	private      bool   // true iff a PRIVATE tag is in use.
	defaultValue *int64 // a default value for INTEGER typed fields (maybe nil).
	tag          *int   // the EXPLICIT or IMPLICIT tag (maybe nil).
	stringType   int    // the string tag to use when marshaling.
	timeType     int    // the time tag to use when marshaling.
	set          bool   // true iff this should be encoded as a SET
	omitEmpty    bool   // true iff this should be omitted if empty when marshaling.

	// Invariants:
	//   if explicit is set, tag is non-nil.
}

// An invalidUnmarshalError describes an invalid argument passed to Unmarshal.
// (The argument to Unmarshal must be a non-nil pointer.)
type invalidUnmarshalError struct {
	Type reflect.Type
}

func (e *invalidUnmarshalError) Error() string {
	if e.Type == nil {
		return "asn1: Unmarshal recipient value is nil"
	}

	if e.Type.Kind() != reflect.Pointer {
		return "asn1: Unmarshal recipient value is non-pointer " + e.Type.String()
	}
	return "asn1: Unmarshal recipient value is nil " + e.Type.String()
}

func getPublicKeyAlgorithmFromOID(oid asn1.ObjectIdentifier) x509.PublicKeyAlgorithm {
	switch {
	case oid.Equal(oidPublicKeyRSA):
		return x509.RSA
	case oid.Equal(oidPublicKeyDSA):
		return x509.DSA
	case oid.Equal(oidPublicKeyECDSA):
		return x509.ECDSA
	case oid.Equal(oidPublicKeyEd25519):
		return x509.Ed25519
	}
	return x509.UnknownPublicKeyAlgorithm
}

func getSignatureAlgorithmFromAI(ai pkix.AlgorithmIdentifier) x509.SignatureAlgorithm {
	if ai.Algorithm.Equal(oidSignatureEd25519) {
		// RFC 8410, Section 3
		// > For all of the OIDs, the parameters MUST be absent.
		if len(ai.Parameters.FullBytes) != 0 {
			return x509.UnknownSignatureAlgorithm
		}
	}

	if !ai.Algorithm.Equal(oidSignatureRSAPSS) {
		for _, details := range signatureAlgorithmDetails {
			if ai.Algorithm.Equal(details.oid) {
				return details.algo
			}
		}
		return x509.UnknownSignatureAlgorithm
	}

	// RSA PSS is special because it encodes important parameters
	// in the Parameters.

	var params pssParameters
	if _, err := nnmarshalASN1(ai.Parameters.FullBytes, &params); err != nil {
		return x509.UnknownSignatureAlgorithm
	}

	var mgf1HashFunc pkix.AlgorithmIdentifier
	if _, err := nnmarshalASN1(params.MGF.Parameters.FullBytes, &mgf1HashFunc); err != nil {
		return x509.UnknownSignatureAlgorithm
	}

	// PSS is greatly overburdened with options. This code forces them into
	// three buckets by requiring that the MGF1 hash function always match the
	// message hash function (as recommended in RFC 3447, Section 8.1), that the
	// salt length matches the hash length, and that the trailer field has the
	// default value.
	if (len(params.Hash.Parameters.FullBytes) != 0 && !bytes.Equal(params.Hash.Parameters.FullBytes, asn1.NullBytes)) ||
		!params.MGF.Algorithm.Equal(oidMGF1) ||
		!mgf1HashFunc.Algorithm.Equal(params.Hash.Algorithm) ||
		(len(mgf1HashFunc.Parameters.FullBytes) != 0 && !bytes.Equal(mgf1HashFunc.Parameters.FullBytes, asn1.NullBytes)) ||
		params.TrailerField != 1 {
		return x509.UnknownSignatureAlgorithm
	}

	switch {
	case params.Hash.Algorithm.Equal(oidSHA256) && params.SaltLength == 32:
		return x509.SHA256WithRSAPSS
	case params.Hash.Algorithm.Equal(oidSHA384) && params.SaltLength == 48:
		return x509.SHA384WithRSAPSS
	case params.Hash.Algorithm.Equal(oidSHA512) && params.SaltLength == 64:
		return x509.SHA512WithRSAPSS
	}

	return x509.UnknownSignatureAlgorithm
}

func parseRawAttributes(rawAttributes []asn1.RawValue) []pkix.AttributeTypeAndValueSET {
	var attributes []pkix.AttributeTypeAndValueSET
	for _, rawAttr := range rawAttributes {
		var attr pkix.AttributeTypeAndValueSET
		rest, err := nnmarshalASN1(rawAttr.FullBytes, &attr)
		// Ignore attributes that don't parse into pkix.AttributeTypeAndValueSET
		// (i.e.: challengePassword or unstructuredName).
		if err == nil && len(rest) == 0 {
			attributes = append(attributes, attr)
		}
	}
	return attributes
}

func parseCertificateRequest(in *certificateRequest) (*x509.CertificateRequest, error) {
	out := &x509.CertificateRequest{
		Raw:                      in.Raw,
		RawTBSCertificateRequest: in.TBSCSR.Raw,
		RawSubjectPublicKeyInfo:  in.TBSCSR.PublicKey.Raw,
		RawSubject:               in.TBSCSR.Subject.FullBytes,

		Signature:          in.SignatureValue.RightAlign(),
		SignatureAlgorithm: getSignatureAlgorithmFromAI(in.SignatureAlgorithm),

		PublicKeyAlgorithm: getPublicKeyAlgorithmFromOID(in.TBSCSR.PublicKey.Algorithm.Algorithm),

		Version:    in.TBSCSR.Version,
		Attributes: parseRawAttributes(in.TBSCSR.RawAttributes),
	}

	var err error
	out.PublicKey, err = parsePublicKey(out.PublicKeyAlgorithm, &in.TBSCSR.PublicKey)
	if err != nil {
		return nil, err
	}

	var subject pkix.RDNSequence
	if rest, err := nnmarshalASN1(in.TBSCSR.Subject.FullBytes, &subject); err != nil {
		return nil, err
	} else if len(rest) != 0 {
		return nil, errors.New("x509: trailing data after X.509 Subject")
	}

	out.Subject.FillFromRDNSequence(&subject)

	if out.Extensions, err = parseCSRExtensions(in.TBSCSR.RawAttributes); err != nil {
		return nil, err
	}

	for _, extension := range out.Extensions {
		switch {
		case extension.Id.Equal(oidExtensionSubjectAltName):
			out.DNSNames, out.EmailAddresses, out.IPAddresses, out.URIs, err = parseSANExtension(extension.Value)
			if err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}

func parsePublicKey(algo x509.PublicKeyAlgorithm, keyData *publicKeyInfo) (any, error) {
	der := cryptobyte.String(keyData.PublicKey.RightAlign())
	switch algo {
	case x509.RSA:
		// RSA public keys must have a NULL in the parameters.
		// See RFC 3279, Section 2.3.1.
		if !bytes.Equal(keyData.Algorithm.Parameters.FullBytes, asn1.NullBytes) {
			return nil, errors.New("x509: RSA key missing NULL parameters")
		}

		p := &pkcs1PublicKey{n: new(big.Int)}
		if !der.ReadASN1(&der, cryptobyte_asn1.SEQUENCE) {
			return nil, errors.New("x509: invalid RSA public key")
		}
		if !der.ReadASN1Integer(p.n) {
			return nil, errors.New("x509: invalid RSA modulus")
		}
		if !der.ReadASN1Integer(&p.e) {
			return nil, errors.New("x509: invalid RSA public exponent")
		}

		if p.n.Sign() <= 0 {
			return nil, errors.New("x509: RSA modulus is not a positive number")
		}
		if p.e <= 0 {
			return nil, errors.New("x509: RSA public exponent is not a positive number")
		}

		pub := &rsa.PublicKey{
			E: p.e,
			N: p.n,
		}
		return pub, nil
	case x509.ECDSA:
		paramsDer := cryptobyte.String(keyData.Algorithm.Parameters.FullBytes)
		namedCurveOID := new(asn1.ObjectIdentifier)
		if !paramsDer.ReadASN1ObjectIdentifier(namedCurveOID) {
			return nil, errors.New("x509: invalid ECDSA parameters")
		}
		namedCurve := namedCurveFromOID(*namedCurveOID)
		if namedCurve == nil {
			return nil, errors.New("x509: unsupported elliptic curve")
		}
		x, y := elliptic.Unmarshal(namedCurve, der)
		if x == nil {
			return nil, errors.New("x509: failed to unmarshal elliptic curve point")
		}
		pub := &ecdsa.PublicKey{
			Curve: namedCurve,
			X:     x,
			Y:     y,
		}
		return pub, nil
	case x509.Ed25519:
		// RFC 8410, Section 3
		// > For all of the OIDs, the parameters MUST be absent.
		if len(keyData.Algorithm.Parameters.FullBytes) != 0 {
			return nil, errors.New("x509: Ed25519 key encoded with illegal parameters")
		}
		if len(der) != ed25519.PublicKeySize {
			return nil, errors.New("x509: wrong Ed25519 public key size")
		}
		return ed25519.PublicKey(der), nil
	case x509.DSA:
		y := new(big.Int)
		if !der.ReadASN1Integer(y) {
			return nil, errors.New("x509: invalid DSA public key")
		}
		pub := &dsa.PublicKey{
			Y: y,
			Parameters: dsa.Parameters{
				P: new(big.Int),
				Q: new(big.Int),
				G: new(big.Int),
			},
		}
		paramsDer := cryptobyte.String(keyData.Algorithm.Parameters.FullBytes)
		if !paramsDer.ReadASN1(&paramsDer, cryptobyte_asn1.SEQUENCE) ||
			!paramsDer.ReadASN1Integer(pub.Parameters.P) ||
			!paramsDer.ReadASN1Integer(pub.Parameters.Q) ||
			!paramsDer.ReadASN1Integer(pub.Parameters.G) {
			return nil, errors.New("x509: invalid DSA parameters")
		}
		if pub.Y.Sign() <= 0 || pub.Parameters.P.Sign() <= 0 ||
			pub.Parameters.Q.Sign() <= 0 || pub.Parameters.G.Sign() <= 0 {
			return nil, errors.New("x509: zero or negative DSA parameter")
		}
		return pub, nil
	default:
		return nil, nil
	}
}

func namedCurveFromOID(oid asn1.ObjectIdentifier) elliptic.Curve {
	switch {
	case oid.Equal(oidNamedCurveP224):
		return elliptic.P224()
	case oid.Equal(oidNamedCurveP256):
		return elliptic.P256()
	case oid.Equal(oidNamedCurveP384):
		return elliptic.P384()
	case oid.Equal(oidNamedCurveP521):
		return elliptic.P521()
	}
	return nil
}

// parseCSRExtensions parses the attributes from a CSR and extracts any
// requested extensions.
func parseCSRExtensions(rawAttributes []asn1.RawValue) ([]pkix.Extension, error) {
	// pkcs10Attribute reflects the Attribute structure from RFC 2986, Section 4.1.
	type pkcs10Attribute struct {
		Id     asn1.ObjectIdentifier
		Values []asn1.RawValue `asn1:"set"`
	}

	var ret []pkix.Extension
	seenExts := make(map[string]bool)
	for _, rawAttr := range rawAttributes {
		var attr pkcs10Attribute
		if rest, err := nnmarshalASN1(rawAttr.FullBytes, &attr); err != nil || len(rest) != 0 || len(attr.Values) == 0 {
			// Ignore attributes that don't parse.
			continue
		}
		oidStr := attr.Id.String()
		if seenExts[oidStr] {
			return nil, errors.New("x509: certificate request contains duplicate extensions")
		}
		seenExts[oidStr] = true

		if !attr.Id.Equal(oidExtensionRequest) {
			continue
		}

		var extensions []pkix.Extension
		if _, err := nnmarshalASN1(attr.Values[0].FullBytes, &extensions); err != nil {
			return nil, err
		}
		requestedExts := make(map[string]bool)
		for _, ext := range extensions {
			oidStr := ext.Id.String()
			if requestedExts[oidStr] {
				return nil, errors.New("x509: certificate request contains duplicate requested extensions")
			}
			requestedExts[oidStr] = true
		}
		ret = append(ret, extensions...)
	}

	return ret, nil
}

func forEachSAN(der cryptobyte.String, callback func(tag int, data []byte) error) error {
	if !der.ReadASN1(&der, cryptobyte_asn1.SEQUENCE) {
		return errors.New("x509: invalid subject alternative names")
	}
	for !der.Empty() {
		var san cryptobyte.String
		var tag cryptobyte_asn1.Tag
		if !der.ReadAnyASN1(&san, &tag) {
			return errors.New("x509: invalid subject alternative name")
		}
		if err := callback(int(tag^0x80), san); err != nil {
			return err
		}
	}

	return nil
}

func parseSANExtension(der cryptobyte.String) (dnsNames, emailAddresses []string, ipAddresses []net.IP, uris []*url.URL, err error) {
	err = forEachSAN(der, func(tag int, data []byte) error {
		switch tag {
		case nameTypeEmail:
			email := string(data)
			if err := isIA5String(email); err != nil {
				return errors.New("x509: SAN rfc822Name is malformed")
			}
			emailAddresses = append(emailAddresses, email)
		case nameTypeDNS:
			name := string(data)
			if err := isIA5String(name); err != nil {
				return errors.New("x509: SAN dNSName is malformed")
			}
			dnsNames = append(dnsNames, string(name))
		case nameTypeURI:
			uriStr := string(data)
			if err := isIA5String(uriStr); err != nil {
				return errors.New("x509: SAN uniformResourceIdentifier is malformed")
			}
			uri, err := url.Parse(uriStr)
			if err != nil {
				return fmt.Errorf("x509: cannot parse URI %q: %s", uriStr, err)
			}
			if len(uri.Host) > 0 {
				if _, ok := domainToReverseLabels(uri.Host); !ok {
					return fmt.Errorf("x509: cannot parse URI %q: invalid domain", uriStr)
				}
			}
			uris = append(uris, uri)
		case nameTypeIP:
			switch len(data) {
			case net.IPv4len, net.IPv6len:
				ipAddresses = append(ipAddresses, data)
			default:
				return errors.New("x509: cannot parse IP address of length " + strconv.Itoa(len(data)))
			}
		}

		return nil
	})

	return
}

func isIA5String(s string) error {
	for _, r := range s {
		// Per RFC5280 "IA5String is limited to the set of ASCII characters"
		if r > unicode.MaxASCII {
			return fmt.Errorf("x509: %q cannot be encoded as an IA5String", s)
		}
	}

	return nil
}

// domainToReverseLabels converts a textual domain name like foo.example.com to
// the list of labels in reverse order, e.g. ["com", "example", "foo"].
func domainToReverseLabels(domain string) (reverseLabels []string, ok bool) {
	for len(domain) > 0 {
		if i := strings.LastIndexByte(domain, '.'); i == -1 {
			reverseLabels = append(reverseLabels, domain)
			domain = ""
		} else {
			reverseLabels = append(reverseLabels, domain[i+1:])
			domain = domain[:i]
		}
	}

	if len(reverseLabels) > 0 && len(reverseLabels[0]) == 0 {
		// An empty label at the end indicates an absolute value.
		return nil, false
	}

	for _, label := range reverseLabels {
		if len(label) == 0 {
			// Empty labels are otherwise invalid.
			return nil, false
		}

		for _, c := range label {
			if c < 33 || c > 126 {
				// Invalid character.
				return nil, false
			}
		}
	}

	return reverseLabels, true
}

func nnmarshalASN1(b []byte, val any) (rest []byte, err error) {
	return unmarshalWithParams(b, val, "")
}

func unmarshalWithParams(b []byte, val any, params string) (rest []byte, err error) {
	v := reflect.ValueOf(val)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil, &invalidUnmarshalError{reflect.TypeOf(val)}
	}
	offset, err := customParseField(v.Elem(), b, 0, customParseFieldParameters(params))
	if err != nil {
		return nil, err
	}
	return b[offset:], nil
}

// Given a tag string with the format specified in the package comment,
// parseFieldParameters will parse it into a fieldParameters structure,
// ignoring unknown parts of the string.
func customParseFieldParameters(str string) (ret fieldParameters) {
	var part string
	for len(str) > 0 {
		part, str, _ = strings.Cut(str, ",")
		switch {
		case part == "optional":
			ret.optional = true
		case part == "explicit":
			ret.explicit = true
			if ret.tag == nil {
				ret.tag = new(int)
			}
		case part == "generalized":
			ret.timeType = asn1.TagGeneralizedTime
		case part == "utc":
			ret.timeType = asn1.TagUTCTime
		case part == "ia5":
			ret.stringType = asn1.TagIA5String
		case part == "printable":
			ret.stringType = asn1.TagPrintableString
		case part == "numeric":
			ret.stringType = asn1.TagNumericString
		case part == "utf8":
			ret.stringType = asn1.TagUTF8String
		case strings.HasPrefix(part, "default:"):
			i, err := strconv.ParseInt(part[8:], 10, 64)
			if err == nil {
				ret.defaultValue = new(int64)
				*ret.defaultValue = i
			}
		case strings.HasPrefix(part, "tag:"):
			i, err := strconv.Atoi(part[4:])
			if err == nil {
				ret.tag = new(int)
				*ret.tag = i
			}
		case part == "set":
			ret.set = true
		case part == "application":
			ret.application = true
			if ret.tag == nil {
				ret.tag = new(int)
			}
		case part == "private":
			ret.private = true
			if ret.tag == nil {
				ret.tag = new(int)
			}
		case part == "omitempty":
			ret.omitEmpty = true
		}
	}
	return
}

// canHaveDefaultValue reports whether k is a Kind that we will set a default
// value for. (A signed integer, essentially.)
func canHaveDefaultValue(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	}

	return false
}

// setDefaultValue is used to install a default value, from a tag string, into
// a Value. It is successful if the field was optional, even if a default value
// wasn't provided or it failed to install it into the Value.
func setDefaultValue(v reflect.Value, params fieldParameters) (ok bool) {
	if !params.optional {
		return
	}
	ok = true
	if params.defaultValue == nil {
		return
	}
	if canHaveDefaultValue(v.Kind()) {
		v.SetInt(*params.defaultValue)
	}
	return
}

// parseField is the main parsing function. Given a byte slice and an offset
// into the array, it will try to parse a suitable ASN.1 value out and store it
// in the given Value.
func customParseField(v reflect.Value, bytes []byte, initOffset int, params fieldParameters) (offset int, err error) {
	offset = initOffset
	fieldType := v.Type()

	// If we have run out of data, it may be that there are optional elements at the end.
	if offset == len(bytes) {
		if !setDefaultValue(v, params) {
			err = asn1.SyntaxError{Msg: "sequence truncated"}
		}
		return
	}

	// Deal with the ANY type.
	if ifaceType := fieldType; ifaceType.Kind() == reflect.Interface && ifaceType.NumMethod() == 0 {
		var t tagAndLength
		t, offset, err = parseTagAndLength(bytes, offset)
		if err != nil {
			return
		}
		if invalidLength(offset, t.length, len(bytes)) {
			err = asn1.SyntaxError{Msg: "data truncated"}
			return
		}
		var result any
		if !t.isCompound && t.class == asn1.ClassUniversal {
			innerBytes := bytes[offset : offset+t.length]
			switch t.tag {
			case asn1.TagPrintableString:
				result, err = parsePrintableString(innerBytes)
			case asn1.TagNumericString:
				result, err = parseNumericString(innerBytes)
			case asn1.TagIA5String:
				result, err = parseIA5String(innerBytes)
			case asn1.TagT61String:
				result, err = parseT61String(innerBytes)
			case asn1.TagUTF8String:
				result, err = parseUTF8String(innerBytes)
			case asn1.TagInteger:
				result, err = parseInt64(innerBytes)
			case asn1.TagBitString:
				result, err = parseBitString(innerBytes)
			case asn1.TagOID:
				result, err = parseObjectIdentifier(innerBytes)
			case asn1.TagUTCTime:
				result, err = parseUTCTime(innerBytes)
			case asn1.TagGeneralizedTime:
				result, err = parseGeneralizedTime(innerBytes)
			case asn1.TagOctetString:
				result = innerBytes
			case asn1.TagBMPString:
				result, err = parseBMPString(innerBytes)
			default:
				// If we don't know how to handle the type, we just leave Value as nil.
			}
		}
		offset += t.length
		if err != nil {
			return
		}
		if result != nil {
			v.Set(reflect.ValueOf(result))
		}
		return
	}

	t, offset, err := parseTagAndLength(bytes, offset)
	if err != nil {
		return
	}
	if params.explicit {
		expectedClass := asn1.ClassContextSpecific
		if params.application {
			expectedClass = asn1.ClassApplication
		}
		if offset == len(bytes) {
			err = asn1.StructuralError{Msg: "explicit tag has no child"}
			return
		}
		if t.class == expectedClass && t.tag == *params.tag && (t.length == 0 || t.isCompound) {
			if fieldType == rawValueType {
				// The inner element should not be parsed for RawValues.
			} else if t.length > 0 {
				t, offset, err = parseTagAndLength(bytes, offset)
				if err != nil {
					return
				}
			} else {
				if fieldType != flagType {
					err = asn1.StructuralError{Msg: "zero length explicit tag was not an asn1.Flag"}
					return
				}
				v.SetBool(true)
				return
			}
		} else {
			// The tags didn't match, it might be an optional element.
			ok := setDefaultValue(v, params)
			if ok {
				offset = initOffset
			} else {
				err = asn1.StructuralError{Msg: "explicitly tagged member didn't match"}
			}
			return
		}
	}

	matchAny, universalTag, compoundType, ok1 := getUniversalType(fieldType)
	if !ok1 {
		err = asn1.StructuralError{Msg: fmt.Sprintf("unknown Go type: %v", fieldType)}
		return
	}

	// Special case for strings: all the ASN.1 string types map to the Go
	// type string. getUniversalType returns the tag for PrintableString
	// when it sees a string, so if we see a different string type on the
	// wire, we change the universal type to match.
	if universalTag == asn1.TagPrintableString {
		if t.class == asn1.ClassUniversal {
			switch t.tag {
			case asn1.TagIA5String, asn1.TagGeneralString, asn1.TagT61String, asn1.TagUTF8String, asn1.TagNumericString, asn1.TagBMPString:
				universalTag = t.tag
			}
		} else if params.stringType != 0 {
			universalTag = params.stringType
		}
	}

	// Special case for time: UTCTime and GeneralizedTime both map to the
	// Go type time.Time.
	if universalTag == asn1.TagUTCTime && t.tag == asn1.TagGeneralizedTime && t.class == asn1.ClassUniversal {
		universalTag = asn1.TagGeneralizedTime
	}

	if params.set {
		universalTag = asn1.TagSet
	}

	matchAnyClassAndTag := matchAny
	expectedClass := asn1.ClassUniversal
	expectedTag := universalTag

	if !params.explicit && params.tag != nil {
		expectedClass = asn1.ClassContextSpecific
		expectedTag = *params.tag
		matchAnyClassAndTag = false
	}

	if !params.explicit && params.application && params.tag != nil {
		expectedClass = asn1.ClassApplication
		expectedTag = *params.tag
		matchAnyClassAndTag = false
	}

	if !params.explicit && params.private && params.tag != nil {
		expectedClass = asn1.ClassPrivate
		expectedTag = *params.tag
		matchAnyClassAndTag = false
	}

	// We have unwrapped any explicit tagging at this point.
	if !matchAnyClassAndTag && (t.class != expectedClass || t.tag != expectedTag) ||
		(!matchAny && t.isCompound != compoundType) {
		// Tags don't match. Again, it could be an optional element.
		ok := setDefaultValue(v, params)
		if ok {
			offset = initOffset
		} else {
			err = asn1.StructuralError{Msg: fmt.Sprintf("tags don't match (%d vs %+v) %+v %s @%d", expectedTag, t, params, fieldType.Name(), offset)}
		}
		return
	}
	if invalidLength(offset, t.length, len(bytes)) {
		err = asn1.SyntaxError{Msg: "data truncated"}
		return
	}
	innerBytes := bytes[offset : offset+t.length]
	offset += t.length

	// We deal with the structures defined in this package first.
	switch v := v.Addr().Interface().(type) {
	case *asn1.RawValue:
		*v = asn1.RawValue{Class: t.class, Tag: t.tag, IsCompound: t.isCompound, Bytes: innerBytes, FullBytes: bytes[initOffset:offset]}
		return
	case *asn1.ObjectIdentifier:
		*v, err = parseObjectIdentifier(innerBytes)
		return
	case *asn1.BitString:
		*v, err = parseBitString(innerBytes)
		return
	case *time.Time:
		if universalTag == asn1.TagUTCTime {
			*v, err = parseUTCTime(innerBytes)
			return
		}
		*v, err = parseGeneralizedTime(innerBytes)
		return
	case *asn1.Enumerated:
		parsedInt, err1 := parseInt32(innerBytes)
		if err1 == nil {
			*v = asn1.Enumerated(parsedInt)
		}
		err = err1
		return
	case *asn1.Flag:
		*v = true
		return
	case **big.Int:
		parsedInt, err1 := parseBigInt(innerBytes)
		if err1 == nil {
			*v = parsedInt
		}
		err = err1
		return
	}
	switch val := v; val.Kind() {
	case reflect.Bool:
		parsedBool, err1 := parseBool(innerBytes)
		if err1 == nil {
			val.SetBool(parsedBool)
		}
		err = err1
		return
	case reflect.Int, reflect.Int32, reflect.Int64:
		if val.Type().Size() == 4 {
			parsedInt, err1 := parseInt32(innerBytes)
			if err1 == nil {
				val.SetInt(int64(parsedInt))
			}
			err = err1
		} else {
			parsedInt, err1 := parseInt64(innerBytes)
			if err1 == nil {
				val.SetInt(parsedInt)
			}
			err = err1
		}
		return
	// TODO(dfc) Add support for the remaining integer types
	case reflect.Struct:
		structType := fieldType

		for i := 0; i < structType.NumField(); i++ {
			if !structType.Field(i).IsExported() {
				err = asn1.StructuralError{Msg: "struct contains unexported fields"}
				return
			}
		}

		if structType.NumField() > 0 &&
			structType.Field(0).Type == rawContentsType {
			bytes := bytes[initOffset:offset]
			val.Field(0).Set(reflect.ValueOf(asn1.RawContent(bytes)))
		}

		innerOffset := 0
		for i := 0; i < structType.NumField(); i++ {
			field := structType.Field(i)
			if i == 0 && field.Type == rawContentsType {
				continue
			}
			innerOffset, err = customParseField(val.Field(i), innerBytes, innerOffset, customParseFieldParameters(field.Tag.Get("asn1")))
			if err != nil {
				return
			}
		}
		// We allow extra bytes at the end of the SEQUENCE because
		// adding elements to the end has been used in X.509 as the
		// version numbers have increased.
		return
	case reflect.Slice:
		sliceType := fieldType
		if sliceType.Elem().Kind() == reflect.Uint8 {
			val.Set(reflect.MakeSlice(sliceType, len(innerBytes), len(innerBytes)))
			reflect.Copy(val, reflect.ValueOf(innerBytes))
			return
		}
		newSlice, err1 := parseSequenceOf(innerBytes, sliceType, sliceType.Elem())
		if err1 == nil {
			val.Set(newSlice)
		}
		err = err1
		return
	case reflect.String:
		var v string
		switch universalTag {
		case asn1.TagPrintableString:
			v, err = parsePrintableString(innerBytes)
		case asn1.TagNumericString:
			v, err = parseNumericString(innerBytes)
		case asn1.TagIA5String:
			v, err = parseIA5String(innerBytes)
		case asn1.TagT61String:
			v, err = parseT61String(innerBytes)
		case asn1.TagUTF8String:
			v, err = parseUTF8String(innerBytes)
		case asn1.TagGeneralString:
			// GeneralString is specified in ISO-2022/ECMA-35,
			// A brief review suggests that it includes structures
			// that allow the encoding to change midstring and
			// such. We give up and pass it as an 8-bit string.
			v, err = parseT61String(innerBytes)
		case asn1.TagBMPString:
			v, err = parseBMPString(innerBytes)

		default:
			err = asn1.SyntaxError{Msg: fmt.Sprintf("internal error: unknown string type %d", universalTag)}
		}
		if err == nil {
			val.SetString(v)
		}
		return
	}
	err = asn1.StructuralError{Msg: "unsupported: " + v.Type().String()}
	return
}

// parseTagAndLength parses an ASN.1 tag and length pair from the given offset
// into a byte slice. It returns the parsed data and the new offset. SET and
// SET OF (tag 17) are mapped to SEQUENCE and SEQUENCE OF (tag 16) since we
// don't distinguish between ordered and unordered objects in this code.
func parseTagAndLength(bytes []byte, initOffset int) (ret tagAndLength, offset int, err error) {
	offset = initOffset
	// parseTagAndLength should not be called without at least a single
	// byte to read. Thus this check is for robustness:
	if offset >= len(bytes) {
		err = errors.New("asn1: internal error in parseTagAndLength")
		return
	}
	b := bytes[offset]
	offset++
	ret.class = int(b >> 6)
	ret.isCompound = b&0x20 == 0x20
	ret.tag = int(b & 0x1f)

	// If the bottom five bits are set, then the tag number is actually base 128
	// encoded afterwards
	if ret.tag == 0x1f {
		ret.tag, offset, err = parseBase128Int(bytes, offset)
		if err != nil {
			return
		}
		// Tags should be encoded in minimal form.
		if ret.tag < 0x1f {
			err = asn1.SyntaxError{Msg: "non-minimal tag"}
			return
		}
	}
	if offset >= len(bytes) {
		err = asn1.SyntaxError{Msg: "truncated tag or length"}
		return
	}
	b = bytes[offset]
	offset++
	if b&0x80 == 0 {
		// The length is encoded in the bottom 7 bits.
		ret.length = int(b & 0x7f)
	} else {
		// Bottom 7 bits give the number of length bytes to follow.
		numBytes := int(b & 0x7f)
		if numBytes == 0 {
			err = asn1.SyntaxError{Msg: "indefinite length found (not DER)"}
			return
		}
		ret.length = 0
		for i := 0; i < numBytes; i++ {
			if offset >= len(bytes) {
				err = asn1.SyntaxError{Msg: "truncated tag or length"}
				return
			}
			b = bytes[offset]
			offset++
			if ret.length >= 1<<23 {
				// We can't shift ret.length up without
				// overflowing.
				err = asn1.StructuralError{Msg: "length too large"}
				return
			}
			ret.length <<= 8
			ret.length |= int(b)
			if ret.length == 0 {
				// DER requires that lengths be minimal.
				err = asn1.StructuralError{Msg: "superfluous leading zeros in length"}
				return
			}
		}
		// Short lengths must be encoded in short form.
		if ret.length < 0x80 {
			err = asn1.StructuralError{Msg: "non-minimal length"}
			return
		}
	}

	return
}

// parseBase128Int parses a base-128 encoded int from the given offset in the
// given byte slice. It returns the value and the new offset.
func parseBase128Int(bytes []byte, initOffset int) (ret, offset int, err error) {
	offset = initOffset
	var ret64 int64
	for shifted := 0; offset < len(bytes); shifted++ {
		// 5 * 7 bits per byte == 35 bits of data
		// Thus the representation is either non-minimal or too large for an int32
		if shifted == 5 {
			err = asn1.StructuralError{Msg: "base 128 integer too large"}
			return
		}
		ret64 <<= 7
		b := bytes[offset]
		// integers should be minimally encoded, so the leading octet should
		// never be 0x80
		if shifted == 0 && b == 0x80 {
			err = asn1.SyntaxError{Msg: "integer is not minimally encoded"}
			return
		}
		ret64 |= int64(b & 0x7f)
		offset++
		if b&0x80 == 0 {
			ret = int(ret64)
			// Ensure that the returned value fits in an int on all platforms
			if ret64 > math.MaxInt32 {
				err = asn1.StructuralError{Msg: "base 128 integer too large"}
			}
			return
		}
	}
	err = asn1.SyntaxError{Msg: "truncated base 128 integer"}
	return
}

// parsePrintableString parses an ASN.1 PrintableString from the given byte
// array and returns it.
func parsePrintableString(bytes []byte) (ret string, err error) {
	for _, b := range bytes {
		if !isPrintable(b, allowAsterisk, allowAmpersand) {
			err = asn1.SyntaxError{Msg: "PrintableString contains invalid character"}
			return
		}
	}
	ret = string(bytes)
	return
}

// invalidLength reports whether offset + length > sliceLength, or if the
// addition would overflow.
func invalidLength(offset, length, sliceLength int) bool {
	return offset+length < offset || offset+length > sliceLength
}

// parseNumericString parses an ASN.1 NumericString from the given byte array
// and returns it.
func parseNumericString(bytes []byte) (ret string, err error) {
	for _, b := range bytes {
		if !isNumeric(b) {
			return "", asn1.SyntaxError{Msg: "NumericString contains invalid character"}
		}
	}
	return string(bytes), nil
}

// isNumeric reports whether the given b is in the ASN.1 NumericString set.
func isNumeric(b byte) bool {
	return '0' <= b && b <= '9' ||
		b == ' '
}

// parseIA5String parses an ASN.1 IA5String (ASCII string) from the given
// byte slice and returns it.
func parseIA5String(bytes []byte) (ret string, err error) {
	for _, b := range bytes {
		if b >= utf8.RuneSelf {
			err = asn1.SyntaxError{Msg: "IA5String contains invalid character"}
			return
		}
	}
	ret = string(bytes)
	return
}

// parseT61String parses an ASN.1 T61String (8-bit clean string) from the given
// byte slice and returns it.
func parseT61String(bytes []byte) (ret string, err error) {
	return string(bytes), nil
}

// parseUTF8String parses an ASN.1 UTF8String (raw UTF-8) from the given byte
// array and returns it.
func parseUTF8String(bytes []byte) (ret string, err error) {
	if !utf8.Valid(bytes) {
		return "", errors.New("asn1: invalid UTF-8 string")
	}
	return string(bytes), nil
}

// parseInt64 treats the given bytes as a big-endian, signed integer and
// returns the result.
func parseInt64(bytes []byte) (ret int64, err error) {
	err = checkInteger(bytes)
	if err != nil {
		return
	}
	if len(bytes) > 8 {
		// We'll overflow an int64 in this case.
		err = asn1.StructuralError{Msg: "integer too large"}
		return
	}
	for bytesRead := 0; bytesRead < len(bytes); bytesRead++ {
		ret <<= 8
		ret |= int64(bytes[bytesRead])
	}

	// Shift up and down in order to sign extend the result.
	ret <<= 64 - uint8(len(bytes))*8
	ret >>= 64 - uint8(len(bytes))*8
	return
}

// checkInteger returns nil if the given bytes are a valid DER-encoded
// INTEGER and an error otherwise.
func checkInteger(bytes []byte) error {
	if len(bytes) == 0 {
		return asn1.StructuralError{Msg: "empty integer"}
	}
	if len(bytes) == 1 {
		return nil
	}
	if (bytes[0] == 0 && bytes[1]&0x80 == 0) || (bytes[0] == 0xff && bytes[1]&0x80 == 0x80) {
		return asn1.StructuralError{Msg: "integer not minimally-encoded"}
	}
	return nil
}

// parseBitString parses an ASN.1 bit string from the given byte slice and returns it.
func parseBitString(bytes []byte) (ret asn1.BitString, err error) {
	if len(bytes) == 0 {
		err = asn1.SyntaxError{Msg: "zero length BIT STRING"}
		return
	}
	paddingBits := int(bytes[0])
	if paddingBits > 7 ||
		len(bytes) == 1 && paddingBits > 0 ||
		bytes[len(bytes)-1]&((1<<bytes[0])-1) != 0 {
		err = asn1.SyntaxError{Msg: "invalid padding bits in BIT STRING"}
		return
	}
	ret.BitLength = (len(bytes)-1)*8 - paddingBits
	ret.Bytes = bytes[1:]
	return
}

// parseObjectIdentifier parses an OBJECT IDENTIFIER from the given bytes and
// returns it. An object identifier is a sequence of variable length integers
// that are assigned in a hierarchy.
func parseObjectIdentifier(bytes []byte) (s asn1.ObjectIdentifier, err error) {
	if len(bytes) == 0 {
		err = asn1.SyntaxError{Msg: "zero length OBJECT IDENTIFIER"}
		return
	}

	// In the worst case, we get two elements from the first byte (which is
	// encoded differently) and then every varint is a single byte long.
	s = make([]int, len(bytes)+1)

	// The first varint is 40*value1 + value2:
	// According to this packing, value1 can take the values 0, 1 and 2 only.
	// When value1 = 0 or value1 = 1, then value2 is <= 39. When value1 = 2,
	// then there are no restrictions on value2.
	v, offset, err := parseBase128Int(bytes, 0)
	if err != nil {
		return
	}
	if v < 80 {
		s[0] = v / 40
		s[1] = v % 40
	} else {
		s[0] = 2
		s[1] = v - 80
	}

	i := 2
	for ; offset < len(bytes); i++ {
		v, offset, err = parseBase128Int(bytes, offset)
		if err != nil {
			return
		}
		s[i] = v
	}
	s = s[0:i]
	return
}

func parseUTCTime(bytes []byte) (ret time.Time, err error) {
	s := string(bytes)

	formatStr := "0601021504Z0700"
	ret, err = time.Parse(formatStr, s)
	if err != nil {
		formatStr = "060102150405Z0700"
		ret, err = time.Parse(formatStr, s)
	}
	if err != nil {
		return
	}

	if serialized := ret.Format(formatStr); serialized != s {
		err = fmt.Errorf("asn1: time did not serialize back to the original value and may be invalid: given %q, but serialized as %q", s, serialized)
		return
	}

	if ret.Year() >= 2050 {
		// UTCTime only encodes times prior to 2050. See https://tools.ietf.org/html/rfc5280#section-4.1.2.5.1
		ret = ret.AddDate(-100, 0, 0)
	}

	return
}

// parseGeneralizedTime parses the GeneralizedTime from the given byte slice
// and returns the resulting time.
func parseGeneralizedTime(bytes []byte) (ret time.Time, err error) {
	const formatStr = "20060102150405Z0700"
	s := string(bytes)

	if ret, err = time.Parse(formatStr, s); err != nil {
		return
	}

	if serialized := ret.Format(formatStr); serialized != s {
		err = fmt.Errorf("asn1: time did not serialize back to the original value and may be invalid: given %q, but serialized as %q", s, serialized)
	}

	return
}

// parseBMPString parses an ASN.1 BMPString (Basic Multilingual Plane of
// ISO/IEC/ITU 10646-1) from the given byte slice and returns it.
func parseBMPString(bmpString []byte) (string, error) {
	if len(bmpString)%2 != 0 {
		return "", errors.New("pkcs12: odd-length BMP string")
	}

	// Strip terminator if present.
	if l := len(bmpString); l >= 2 && bmpString[l-1] == 0 && bmpString[l-2] == 0 {
		bmpString = bmpString[:l-2]
	}

	s := make([]uint16, 0, len(bmpString)/2)
	for len(bmpString) > 0 {
		s = append(s, uint16(bmpString[0])<<8+uint16(bmpString[1]))
		bmpString = bmpString[2:]
	}

	return string(utf16.Decode(s)), nil
}

// Given a reflected Go type, getUniversalType returns the default tag number
// and expected compound flag.
func getUniversalType(t reflect.Type) (matchAny bool, tagNumber int, isCompound, ok bool) {
	switch t {
	case rawValueType:
		return true, -1, false, true
	case objectIdentifierType:
		return false, asn1.TagOID, false, true
	case bitStringType:
		return false, asn1.TagBitString, false, true
	case timeType:
		return false, asn1.TagUTCTime, false, true
	case enumeratedType:
		return false, asn1.TagEnum, false, true
	case bigIntType:
		return false, asn1.TagInteger, false, true
	}
	switch t.Kind() {
	case reflect.Bool:
		return false, asn1.TagBoolean, false, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return false, asn1.TagInteger, false, true
	case reflect.Struct:
		return false, asn1.TagSequence, true, true
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return false, asn1.TagOctetString, false, true
		}
		if strings.HasSuffix(t.Name(), "SET") {
			return false, asn1.TagSet, true, true
		}
		return false, asn1.TagSequence, true, true
	case reflect.String:
		return false, asn1.TagPrintableString, false, true
	}
	return false, 0, false, false
}

// parseInt treats the given bytes as a big-endian, signed integer and returns
// the result.
func parseInt32(bytes []byte) (int32, error) {
	if err := checkInteger(bytes); err != nil {
		return 0, err
	}
	ret64, err := parseInt64(bytes)
	if err != nil {
		return 0, err
	}
	if ret64 != int64(int32(ret64)) {
		return 0, asn1.StructuralError{Msg: "integer too large"}
	}
	return int32(ret64), nil
}

var bigOne = big.NewInt(1)

// parseBigInt treats the given bytes as a big-endian, signed integer and returns
// the result.
func parseBigInt(bytes []byte) (*big.Int, error) {
	if err := checkInteger(bytes); err != nil {
		return nil, err
	}
	ret := new(big.Int)
	if len(bytes) > 0 && bytes[0]&0x80 == 0x80 {
		// This is a negative number.
		notBytes := make([]byte, len(bytes))
		for i := range notBytes {
			notBytes[i] = ^bytes[i]
		}
		ret.SetBytes(notBytes)
		ret.Add(ret, bigOne)
		ret.Neg(ret)
		return ret, nil
	}
	ret.SetBytes(bytes)
	return ret, nil
}

func parseBool(bytes []byte) (ret bool, err error) {
	if len(bytes) != 1 {
		err = asn1.SyntaxError{Msg: "invalid boolean"}
		return
	}

	// DER demands that "If the encoding represents the boolean value TRUE,
	// its single contents octet shall have all eight bits set to one."
	// Thus only 0 and 255 are valid encoded values.
	switch bytes[0] {
	case 0:
		ret = false
	case 0xff:
		ret = true
	default:
		err = asn1.SyntaxError{Msg: "invalid boolean"}
	}

	return
}

// parseSequenceOf is used for SEQUENCE OF and SET OF values. It tries to parse
// a number of ASN.1 values from the given byte slice and returns them as a
// slice of Go values of the given type.
func parseSequenceOf(bytes []byte, sliceType reflect.Type, elemType reflect.Type) (ret reflect.Value, err error) {
	matchAny, expectedTag, compoundType, ok := getUniversalType(elemType)
	if !ok {
		err = asn1.StructuralError{Msg: "unknown Go type for slice"}
		return
	}

	// First we iterate over the input and count the number of elements,
	// checking that the types are correct in each case.
	numElements := 0
	for offset := 0; offset < len(bytes); {
		var t tagAndLength
		t, offset, err = parseTagAndLength(bytes, offset)
		if err != nil {
			return
		}
		switch t.tag {
		case asn1.TagIA5String, asn1.TagGeneralString, asn1.TagT61String, asn1.TagUTF8String, asn1.TagNumericString, asn1.TagBMPString:
			// We pretend that various other string types are
			// PRINTABLE STRINGs so that a sequence of them can be
			// parsed into a []string.
			t.tag = asn1.TagPrintableString
		case asn1.TagGeneralizedTime, asn1.TagUTCTime:
			// Likewise, both time types are treated the same.
			t.tag = asn1.TagUTCTime
		}

		if !matchAny && (t.class != classUniversal || t.isCompound != compoundType || t.tag != expectedTag) {
			err = asn1.StructuralError{Msg: "sequence tag mismatch"}
			return
		}
		if invalidLength(offset, t.length, len(bytes)) {
			err = asn1.SyntaxError{Msg: "truncated sequence"}
			return
		}
		offset += t.length
		numElements++
	}
	ret = reflect.MakeSlice(sliceType, numElements, numElements)
	params := fieldParameters{}
	offset := 0
	for i := 0; i < numElements; i++ {
		offset, err = customParseField(ret.Index(i), bytes, offset, params)
		if err != nil {
			return
		}
	}
	return
}
