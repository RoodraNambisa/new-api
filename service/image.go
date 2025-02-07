package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"image"
	"io"
	"one-api/common"
	"strings"
	"one-api/dto"
	"golang.org/x/image/webp"
)

func DecodeBase64ImageData(base64String string) (image.Config, string, string, error) {
	// 去除base64数据的URL前缀（如果有）
	if idx := strings.Index(base64String, ","); idx != -1 {
		base64String = base64String[idx+1:]
	}

	// 将base64字符串解码为字节切片
	decodedData, err := base64.StdEncoding.DecodeString(base64String)
	if err != nil {
		fmt.Println("Error: Failed to decode base64 string")
		return image.Config{}, "", "", err
	}

	// 创建一个bytes.Buffer用于存储解码后的数据
	reader := bytes.NewReader(decodedData)
	config, format, err := getImageConfig(reader)
	return config, format, base64String, err
}

func DecodeBase64FileData(base64String string) (string, string, error) {
	var mimeType string
	var idx int
	idx = strings.Index(base64String, ",")
	if idx == -1 {
		_, file_type, base64, err := DecodeBase64ImageData(base64String)
		return "image/" + file_type, base64, err
	}
	mimeType = base64String[:idx]
	base64String = base64String[idx+1:]
	idx = strings.Index(mimeType, ";")
	if idx == -1 {
		_, file_type, base64, err := DecodeBase64ImageData(base64String)
		return "image/" + file_type, base64, err
	}
	mimeType = mimeType[:idx]
	idx = strings.Index(mimeType, ":")
	if idx == -1 {
		_, file_type, base64, err := DecodeBase64ImageData(base64String)
		return "image/" + file_type, base64, err
	}
	mimeType = mimeType[idx+1:]
	return mimeType, base64String, nil
}

// GetImageFromUrl 获取图片的类型和base64编码的数据
func GetImageFromUrl(url string) (mimeType string, data string, err error) {
	resp, err := DoDownloadRequest(url)
	if err != nil {
		return "", "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("fail to get image from url: %s", resp.Status)
	}

	// 通过读取部分数据检测图片的MIME类型
	sniff := make([]byte, 512)
	n, err := io.ReadFull(resp.Body, sniff)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", "", err
	}
	mimeTypeDetected := http.DetectContentType(sniff[:n])
	if !strings.HasPrefix(mimeTypeDetected, "image/") {
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
			return "", "", fmt.Errorf("invalid content type: %s, required image/*", mimeTypeDetected)
		}
	}

	// 将已读数据写回buffer，读剩余数据
	buffer := bytes.NewBuffer(sniff[:n])
	_, err = buffer.ReadFrom(resp.Body)
	if err != nil {
		return "", "", err
	}

	mimeType = mimeTypeDetected
	data = base64.StdEncoding.EncodeToString(buffer.Bytes())
	return
}

func DecodeUrlImageData(imageUrl string) (image.Config, string, error) {
	response, err := DoDownloadRequest(imageUrl)
	if err != nil {
		common.SysLog(fmt.Sprintf("fail to get image from url: %s", err.Error()))
		return image.Config{}, "", err
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		err = errors.New(fmt.Sprintf("fail to get image from url: %s", response.Status))
		return image.Config{}, "", err
	}
	
	sniffData := make([]byte, 512)
	
	n, readErr := io.ReadFull(response.Body, sniffData)
	if readErr != nil && readErr != io.ErrUnexpectedEOF {
		return image.Config{}, "", readErr
	}

	readData := sniffData[:n]
	mimeType := http.DetectContentType(readData)
    	if !strings.HasPrefix(mimeType, "image/") {
		if !strings.HasPrefix(response.Header.Get("Content-Type"), "image/") {
			return image.Config{}, "", fmt.Errorf("invalid content type: %s, required image/*", mimeType)
		}
	}
	
	for _, limit := range []int64{1024 * 8, 1024 * 24, 1024 * 64} {
		common.SysLog(fmt.Sprintf("try to decode image config with limit: %d", limit))

		// 从response.Body读取更多的数据直到达到当前的限制
		additionalData := make([]byte, limit-int64(len(readData)))
		n, _ := io.ReadFull(response.Body, additionalData)
		readData = append(readData, additionalData[:n]...)

		// 使用io.MultiReader组合已经读取的数据和response.Body
		limitReader := io.MultiReader(bytes.NewReader(readData), response.Body)
		var config image.Config
		var format string
		config, format, err = getImageConfig(limitReader)
		if err == nil {
			return config, format, nil
		}
	}

	return image.Config{}, "", err // 返回最后一个错误
}

func getImageConfig(reader io.Reader) (image.Config, string, error) {
	// 读取图片的头部信息来获取图片尺寸
	config, format, err := image.DecodeConfig(reader)
	if err != nil {
		err = errors.New(fmt.Sprintf("fail to decode image config(gif, jpg, png): %s", err.Error()))
		common.SysLog(err.Error())
		config, err = webp.DecodeConfig(reader)
		if err != nil {
			err = errors.New(fmt.Sprintf("fail to decode image config(webp): %s", err.Error()))
			common.SysLog(err.Error())
		}
		format = "webp"
	}
	if err != nil {
		return image.Config{}, "", err
	}
	return config, format, nil
}

func ConvertImageUrlsToBase64(m *dto.Message) {
    if m.IsStringContent() {
        return
    }
	contentList := m.ParseContent()
	for i, cItem := range contentList {
		if cItem.Type == dto.ContentTypeImageURL {
			if urlValue, ok := cItem.ImageUrl.(dto.MessageImageUrl); ok {
				if !strings.HasPrefix(urlValue.Url, "data:") &&
					(strings.HasPrefix(urlValue.Url, "http://") || strings.HasPrefix(urlValue.Url, "https://")) {
					mimeType, base64Data, err := GetImageFromUrl(urlValue.Url)
					if err == nil && base64Data != "" {
						urlValue.Url = fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data)
						contentList[i].ImageUrl = urlValue
					}
				}
			}
		}
	}
	newContentBytes, _ := json.Marshal(contentList)
	m.Content = newContentBytes
}