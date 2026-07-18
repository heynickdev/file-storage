package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find jwt", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("Uploading thumbnail for video ", videoID, "by user", userID)

	const maxMemory = 1 << 30
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "incorrect header", err)
		return
	}

	file, fileHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "incorrect header", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(fileHeader.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Incorrect header", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Incorrect header", err)
		return
	}

	metaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "video cannot be found", err)
		return
	}
	if metaData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You are not the owner", err)
		return
	}

	tmpSlug := fmt.Sprintf("%v.mp4", videoID)
	// ====================================================================================================================
	// CREATE FILE
	// ====================================================================================================================
	tmp, err := os.CreateTemp(cfg.filepathRoot, tmpSlug)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create temporary file", err)
		return
	}

	defer tmp.Close()
	defer os.Remove(tmp.Name())

	// ====================================================================================================================
	// COPY AND ADD SEEK
	// ====================================================================================================================

	if _, err := io.Copy(tmp, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to copy data", err)
		return
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to start file from beginning for reading", err)
		return
	}

	// ====================================================================================================================
	// RATIO
	// ====================================================================================================================

	ratio, err := getVideoAspectRatio(tmp.Name())

	tmpSlug = fmt.Sprintf("%v/%v", ratio, tmpSlug)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get aspect ratio", err)
		return
	}

	// ====================================================================================================================
	// ADD FAST START
	// ====================================================================================================================

	nFilePath, err := processVideoForFastStart(tmp.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to add movflags", err)
		return
	}
	nFile, err := os.Open(nFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to opne file", err)
		return
	}

	// ====================================================================================================================
	// SEND TO S3
	// ====================================================================================================================

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &tmpSlug,
		Body:        nFile,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to upload to aws", err)
		return
	}
	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to get video form the ID", err)
		return
	}
    key := fmt.Sprintf("%v,%v", cfg.s3Bucket, tmpSlug)
	videoData.VideoURL = &key
	if err := cfg.db.UpdateVideo(videoData); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save video to database", err)
		return
	}
    pVideo, err := cfg.dbVideoToSignedVideo(videoData)
    if err != nil {
        respondWithError(w, http.StatusBadRequest, "unable to parse video", err)
        return
    }
	respondWithJSON(w, http.StatusOK, pVideo)
}

func getVideoAspectRatio(filepath string) (string, error) {
	ffcmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)
	data := new(bytes.Buffer)
	ffcmd.Stdout = data
	if err := ffcmd.Run(); err != nil {
		return "", err
	}
	type aspect struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	aspectR := aspect{}

	if err := json.Unmarshal(data.Bytes(), &aspectR); err != nil {
		return "", err
	}
	if aspectR.Streams[0].Width > aspectR.Streams[0].Height {
		return "landscape", nil
	} else if aspectR.Streams[0].Height > aspectR.Streams[0].Width {
		return "portrait", nil
	}
	return "other", nil
}

func processVideoForFastStart(filePath string) (string, error) {
	nFilePath := fmt.Sprintf("%v.processing", filePath)
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", nFilePath)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("unable to change moveflag: %v", err)
	}
	return nFilePath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	newS3 := s3.NewPresignClient(s3Client)
    httpR, err := newS3.PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}, s3.WithPresignExpires(expireTime))
    if err != nil {
        return "", fmt.Errorf("unable to get HTTP request: %v", err)
    }
	return httpR.URL, nil
}
