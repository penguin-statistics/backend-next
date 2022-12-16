package service

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/pkg/errors"

	"exusiai.dev/backend-next/internal/config"
	"exusiai.dev/backend-next/internal/model/types"
)

const (
	UpyunAuthorizationHeaderRealm = "UPYUN"
)

type Upyun struct {
	operatorUsername string
	operatorPassword string
	ugcBucket        string
	notifyURLPrefix  string
	cdnDomain        string
	signatureSecret  string
}

func NewUpyun(conf *config.Config) *Upyun {
	return &Upyun{
		operatorUsername: conf.UpyunOperatorName,
		operatorPassword: conf.UpyunOperatorPassword,
		ugcBucket:        conf.UpyunUserContentBucket,
		notifyURLPrefix:  conf.UpyunNotifyURLPrefix,
		cdnDomain:        conf.UpyunUserContentDomain,
		signatureSecret:  conf.UpyunUserContentSignatureSecret,
	}
}

type upyunPolicy struct {
	Bucket     string `json:"bucket"`
	SaveKey    string `json:"save-key"`
	Expiration int64  `json:"expiration"`
	Date       string `json:"date"`
	NotifyURL  string `json:"notify-url,omitempty"`
	AllowFile  string `json:"allow-file-type,omitempty"`
	ContentLen string `json:"content-length-range,omitempty"`
}

func (c *Upyun) InitImageUpload(prefix string, uploadId string) (types.UpyunInitResponse, error) {
	now := time.Now().UTC()
	policy := upyunPolicy{
		Bucket:     c.ugcBucket,
		SaveKey:    "/" + prefix + "/{year}-{mon}/" + uploadId + "_{filemd5}{.suffix}",
		Expiration: now.Add(time.Minute * 30).Unix(),
		Date:       now.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		NotifyURL:  c.notifyURLPrefix + "/" + uploadId,
		AllowFile:  "jpg,jpeg,png,heif",
		ContentLen: "0,20971520", // 0 - 20MB
	}
	authorization, policyBase64, err := c.calculate(policy)
	if err != nil {
		return types.UpyunInitResponse{}, err
	}

	return types.UpyunInitResponse{
		URL:           "https://v0.api.upyun.com/" + c.ugcBucket,
		Authorization: authorization,
		Policy:        policyBase64,
	}, nil
}

func (c *Upyun) calculate(policyObj upyunPolicy) (authorization, policy string, err error) {
	policyJson, err := json.Marshal(policyObj)
	if err != nil {
		return "", "", err
	}

	policyBase64 := base64.StdEncoding.EncodeToString(policyJson)

	md5Password := md5.Sum([]byte(c.operatorPassword))
	md5HexPassword := hex.EncodeToString(md5Password[:])

	policySignatureString := "POST" + "&" + ("/" + c.ugcBucket) + "&" + policyObj.Date + "&" + policyBase64

	hmacSha1 := hmac.New(sha1.New, []byte(md5HexPassword))
	hmacSha1.Write([]byte(policySignatureString))
	signatureBytes := hmacSha1.Sum(nil)

	signature := base64.StdEncoding.EncodeToString(signatureBytes)
	authorization = UpyunAuthorizationHeaderRealm + " " + c.operatorUsername + ":" + signature

	return authorization, policyBase64, nil
}

func (c *Upyun) VerifyImageUploadCallback(ctx *fiber.Ctx) (path string, err error) {
	body := ctx.Body()
	v, err := url.ParseQuery(string(body))
	if err != nil {
		return "", errors.Errorf("failed to parse body: %w", err)
	}

	if v.Get("code") != "200" {
		return "", errors.Errorf("upyun upload failed: %s", v.Get("message"))
	}

	timeStr := v.Get("time")
	timeInt, err := strconv.Atoi(timeStr)
	if err != nil {
		return "", errors.Errorf("failed to parse time: %w", err)
	}
	timeT := time.Unix(int64(timeInt), 0)
	if timeT.Before(time.Now().Add(-time.Minute * 10)) {
		return "", errors.Errorf("upyun upload time expired")
	}

	authorization := ctx.Get("Authorization")

	if !strings.HasPrefix(authorization, UpyunAuthorizationHeaderRealm) {
		return "", errors.Errorf("invalid authorization: missing correct header realm in Authorization header")
	}

	authorization = strings.TrimPrefix(authorization, UpyunAuthorizationHeaderRealm)
	authorization = strings.TrimSpace(authorization)
	segments := strings.Split(authorization, ":")
	if len(segments) != 2 {
		return "", errors.Errorf("invalid authorization: invalid segments")
	}

	username := segments[0]
	signature := segments[1]
	if username != c.operatorUsername {
		return "", errors.Errorf("invalid authorization: unknown username")
	}

	md5Password := md5.Sum([]byte(c.operatorPassword))
	md5HexPassword := hex.EncodeToString(md5Password[:])

	md5Body := md5.Sum(body)
	md5HexBody := hex.EncodeToString(md5Body[:])

	signatureString := "POST" + "&" + ctx.Path() + "&" + ctx.Get("Date") + "&" + md5HexBody

	hmacSha1 := hmac.New(sha1.New, []byte(md5HexPassword))
	hmacSha1.Write([]byte(signatureString))

	signatureBytes := hmacSha1.Sum(nil)
	signatureBase64 := base64.StdEncoding.EncodeToString(signatureBytes)

	if signature != signatureBase64 {
		return "", errors.Errorf("invalid authorization: signature mismatch")
	}

	u := v.Get("url")
	if u == "" {
		return "", errors.Errorf("invalid url")
	}

	return u, nil
}

func (c *Upyun) MarshalImageURI(path string) string {
	var u url.URL
	u.Scheme = "upyun"
	u.Host = c.ugcBucket
	u.Path = path
	return u.String()
}

func (c *Upyun) ImageURIToSignedURL(uri, style string) (path string, err error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	if u.Scheme != "upyun" {
		return "", errors.Errorf("invalid scheme")
	}

	if u.Host != c.ugcBucket {
		return "", errors.Errorf("invalid host")
	}

	if style != "" {
		u.Path += "!" + style
	}

	u.Host = c.cdnDomain
	u.Scheme = "https"

	etime := time.Now().UTC().Add(time.Minute * 10).Unix()
	token := c.calculateSignature(u.Path, etime)

	u.RawQuery = url.Values{
		"_upt": []string{token},
	}.Encode()

	return u.String(), nil
}

func (c *Upyun) calculateSignature(path string, etime int64) string {
	signatureString := fmt.Sprintf("%s&%d&%s", c.signatureSecret, etime, path)
	md5Bytes := md5.Sum([]byte(signatureString))
	md5Hex := hex.EncodeToString(md5Bytes[:])
	// get the middle 16 bytes
	signature := md5Hex[12:20]
	return fmt.Sprintf("%s%d", signature, etime)
}
