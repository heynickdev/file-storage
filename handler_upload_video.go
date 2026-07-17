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

	tmpSlug:= fmt.Sprintf("%v.mp4", videoID)
	tmp, err := os.CreateTemp(cfg.filepathRoot, tmpSlug)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create temporary file", err)
		return
	}

	defer tmp.Close()
	defer os.Remove(tmp.Name())


	if _, err := io.Copy(tmp, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to copy data", err)
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to start file from beginning for reading", err)
		return
	}

	ratio, err := getVideoAspectRatio(tmp.Name())
	tmpSlug = fmt.Sprintf("%v/%v", ratio, tmpSlug)
	fmt.Println(ratio)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get aspect ratio", err)
		return
	}

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &tmpSlug,
		Body:        tmp,
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
	videoURL := fmt.Sprintf("https://%v.s3.%v.amazonaws.com/%v", cfg.s3Bucket, cfg.s3Region, tmpSlug)
	videoData.VideoURL = &videoURL
	if err := cfg.db.UpdateVideo(videoData); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save video to database", err)
		return
	}
	respondWithJSON(w, http.StatusOK, videoData)
}

func getVideoAspectRatio(filepath string) (string, error) {
	ffcmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)
	data := new(bytes.Buffer)
	ffcmd.Stdout = data
	if err := ffcmd.Run(); err != nil {
		return "", err
	}
	type aspect struct {
		Streams []struct{
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"` 

	}
	aspectR := aspect{}

	if err := json.Unmarshal(data.Bytes(), &aspectR); err != nil {
		return "", err
	}
	fmt.Println(aspectR.Streams[0].Height)
	fmt.Println(aspectR.Streams[0].Width)
	if aspectR.Streams[0].Width > aspectR.Streams[0].Height {
		return "landscape", nil
	} else if aspectR.Streams[0].Height > aspectR.Streams[0].Width {
		return "portrait", nil
	}
	return "other", nil
}

