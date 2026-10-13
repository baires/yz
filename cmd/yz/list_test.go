package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/history"
)

func TestDeleteShareSiteAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yz")
	if err := config.Save(dir, &config.Config{
		AccountID: fakes.AccountID, Bucket: "photos", URLBase: "https://cdn.example.com",
		S3AccessKeyID: fakes.AccessKey, S3Secret: fakes.SecretKey,
	}); err != nil {
		t.Fatal(err)
	}
	s3 := fakes.NewS3Server(t)
	env := envConfig{ConfigDir: dir, R2Endpoint: s3.URL()}
	site := "sites/AAAAAAAAAAAAAAAA/"
	siteKeys := []string{"index.html", "app.js", "css/a.css", "img/b.png", "c/d/e.txt"}
	for _, k := range siteKeys {
		s3.PutObjectDirect(site+k, []byte("x"))
	}
	s3.PutObjectDirect("sites/AAAAAAAAAAAAAAAB/index.html", []byte("other site"))
	s3.PutObjectDirect("BBBBBBBBBBBBBBBB/photo.png", []byte("img"))
	s3.PutObjectDirect("CCCCCCCCCCCCCCCC/keep.png", []byte("keep"))
	now := time.Now().Truncate(time.Second)
	keep := history.Entry{URL: "https://cdn.example.com/CCCCCCCCCCCCCCCC/keep.png", File: "keep.png", CreatedAt: now.Add(-3 * time.Minute)}
	file := history.Entry{URL: "https://cdn.example.com/BBBBBBBBBBBBBBBB/photo.png", File: "photo.png", CreatedAt: now.Add(-2 * time.Minute)}
	siteEntry := history.Entry{Kind: history.KindSite, URL: "https://cdn.example.com/s/AAAAAAAAAAAAAAAA/", File: "dist", CreatedAt: now.Add(-time.Minute)}
	for _, e := range []history.Entry{keep, file, siteEntry} {
		if err := history.Add(dir, e); err != nil {
			t.Fatal(err)
		}
	}

	if err := deleteShare(context.Background(), env, siteEntry); err != nil {
		t.Fatalf("delete site: %v", err)
	}
	for _, k := range siteKeys {
		if _, ok := s3.GetObject(site + k); ok {
			t.Errorf("site object %s survived", k)
		}
	}
	lists := 0
	for _, c := range s3.Calls() {
		if strings.Contains(c, "list-type=2") {
			lists++
		}
	}
	if lists != 3 { // 5 keys at fakes.ListPageSize=2 per page
		t.Errorf("list pages = %d", lists)
	}
	if err := deleteShare(context.Background(), env, file); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if _, ok := s3.GetObject("BBBBBBBBBBBBBBBB/photo.png"); ok {
		t.Error("file share survived")
	}
	for _, k := range []string{"sites/AAAAAAAAAAAAAAAB/index.html", "CCCCCCCCCCCCCCCC/keep.png"} {
		if _, ok := s3.GetObject(k); !ok {
			t.Errorf("unrelated object %s deleted", k)
		}
	}
	entries, _ := history.Load(dir)
	if len(entries) != 1 || entries[0].File != "keep.png" {
		t.Errorf("history = %+v", entries)
	}
}

func TestDeleteShareKeepsHistoryOnFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yz")
	_ = config.Save(dir, &config.Config{
		AccountID: fakes.AccountID, Bucket: "photos", URLBase: "https://cdn.example.com",
		S3AccessKeyID: fakes.AccessKey, S3Secret: fakes.SecretKey,
	})
	s3 := fakes.NewS3Server(t)
	s3.StatusOverride = 403
	e := history.Entry{URL: "https://cdn.example.com/BBBBBBBBBBBBBBBB/photo.png", File: "photo.png", CreatedAt: time.Now().Truncate(time.Second)}
	_ = history.Add(dir, e)
	if err := deleteShare(context.Background(), envConfig{ConfigDir: dir, R2Endpoint: s3.URL()}, e); err == nil {
		t.Fatal("rejected delete reported success")
	}
	if entries, _ := history.Load(dir); len(entries) != 1 {
		t.Error("history dropped after failed delete")
	}
}
