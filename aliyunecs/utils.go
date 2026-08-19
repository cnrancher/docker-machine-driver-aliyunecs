package aliyunecs

import (
	"crypto/md5"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/rancher/machine/libmachine/log"
)

var (
	errInvalidRegion  = errors.New("invalid region specified")
	errNoVpcs         = errors.New("No VPCs found in region")
	errMachineFailure = errors.New("Machine failed to start")
	errNoIP           = errors.New("No IP Address associated with the instance")
	errComplete       = errors.New("Complete")
)

const defaultUbuntuImageID = "ubuntu_22_04_x64_20G_alibase_20240807.vhd"
const defaultUbuntuImagePrefix = "ubuntu_22_04_x64"

func validateECSRegion(region string) (Region, error) {
	for _, v := range validRegions {
		if v == Region(region) {
			return v, nil
		}
	}

	return "", errInvalidRegion
}

const digitals = "0123456789"
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const specialChars = "()`~!@#$%^&*-+=|{}[]:;'<>,.?/"
const dictionary = digitals + alphabet + specialChars
const tokenDictionary = "_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const paswordLen = 16

func RandomPassword() string {
	var bytes = make([]byte, paswordLen)
	rand.Read(bytes)
	for k, v := range bytes {
		var ch byte
		switch k {
		case 0:
			ch = alphabet[v%byte(len(alphabet))]
		case 1:
			ch = digitals[v%byte(len(digitals))]
		case 2:
			ch = specialChars[v%byte(len(specialChars))]
		default:
			ch = dictionary[v%byte(len(dictionary))]
		}
		bytes[k] = ch
	}
	return string(bytes)
}

func isUbuntuImage(image string) bool {
	return strings.HasPrefix(image, "ubuntu")
}

// isInstanceNotFound reports whether err means the ECS instance no longer exists.
// Treat this as successful removal so rancher-machine delete jobs do not retry forever.
func isInstanceNotFound(err error) bool {
	if err == nil {
		return false
	}
	var se *tea.SDKError
	if errors.As(err, &se) {
		code := tea.StringValue(se.Code)
		if code == "InvalidInstanceId.NotFound" || code == "InvalidInstance.NotFound" {
			return true
		}
		if se.StatusCode != nil && *se.StatusCode == 404 {
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "InvalidInstanceId.NotFound") ||
		strings.Contains(msg, "InvalidInstance.NotFound") ||
		strings.Contains(msg, "StatusCode: 404")
}

func SplitPortProto(raw string) (port int, protocol string, err error) {
	parts := strings.SplitN(raw, "/", 2)
	out, err := strconv.Atoi(parts[0])
	if err != nil {
		return 22, "tcp", err
	}
	if len(parts) == 1 {
		return out, "tcp", nil
	}
	if parts[1] != "tcp" && parts[1] != "udp" && parts[1] != "all" && parts[1] != "icmp" && parts[1] != "gre" {
		// If the format passed in does not match the ecs communication protocol then the default is tcp
		return out, "tcp", nil
	}

	return out, parts[1], nil
}

// A PaginationResponse represents a response with pagination information
type PaginationResult struct {
	TotalCount int
	PageNumber int
	PageSize   int
}

type Pagination struct {
	PageNumber int
	PageSize   int
}

// NextPage gets the next page of the result set
func (r *PaginationResult) NextPage() *Pagination {
	if r.PageNumber*r.PageSize >= r.TotalCount {
		return nil
	}
	return &Pagination{PageNumber: r.PageNumber + 1, PageSize: r.PageSize}
}

var validRegions = []Region{
	Hangzhou, Qingdao, Beijing, Shenzhen, Hongkong, Shanghai, Zhangjiakou, Huhehaote,
	USWest1, USEast1,
	APNorthEast1, APSouthEast1, APSouthEast2, APSouthEast3, APSouthEast5,
	APSouth1,
	MEEast1,
	EUCentral1, EUWest1,
	ShenZhenFinance, ShanghaiFinance,
}

// CreateRandomString create random string
func CreateRandomString() string {
	b := make([]byte, 32)
	l := len(tokenDictionary)
	_, err := rand.Read(b)
	if err != nil {
		// fail back to insecure rand
		mrand.Seed(time.Now().UnixNano())
		for i := range b {
			b[i] = tokenDictionary[mrand.Int()%l]
		}
	} else {
		for i, v := range b {
			b[i] = dictionary[v%byte(l)]
		}
	}
	return string(b)
}

func GetContainerCIDR(cidrBlock string) (string, error) {
	ip, _, err := net.ParseCIDR(cidrBlock)
	if err != nil {
		return "", err
	}
	ip = ip.To4()
	ip[2] = 0
	ip[3] = 0
	return fmt.Sprintf("%s/16", ip.String()), nil
}

func generateId() string {
	rb := make([]byte, 10)
	_, err := rand.Read(rb)
	if err != nil {
		log.Errorf("Unable to generate id: %s", err)
	}
	h := md5.New()
	io.WriteString(h, string(rb))
	return fmt.Sprintf("%x", h.Sum(nil))
}
