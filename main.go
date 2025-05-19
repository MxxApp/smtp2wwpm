package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"
)

// 发送HTML消息到 webhook
func sendHTMLToWebhook(webhookURL, subject, html string) {
	payload := map[string]interface{}{
		"msgtype": "html",
		"html": map[string]string{
			"title":   subject,
			"content": html,
		},
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(data))
	if err != nil {
		log.Printf("[POST] create req error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[POST] send error: %v", err)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[POST] status: %s, subject: %s, resp: %s", resp.Status, subject, string(respBody))
}

// 解码邮件正文（支持 base64/quoted-printable/7bit/8bit）
func decodeBody(body []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(encoding) {
	case "base64":
		return base64.StdEncoding.DecodeString(strings.ReplaceAll(string(body), "\r\n", ""))
	case "quoted-printable":
		r := quotedprintable.NewReader(bytes.NewReader(body))
		return io.ReadAll(r)
	case "7bit", "8bit", "":
		return body, nil
	default:
		return body, nil
	}
}

// 递归解析邮件，优先返回 text/html，fallback 到 text/plain，忽略附件
func extractMailContent(msg *mail.Message) (subject, html string, attachments []string) {
	subject = msg.Header.Get("Subject")
	contentType := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	body, _ := io.ReadAll(msg.Body)

	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		var htmlBody, plainBody string
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			ct := p.Header.Get("Content-Type")
			cd := p.Header.Get("Content-Disposition")
			cte := p.Header.Get("Content-Transfer-Encoding")
			partBody, _ := io.ReadAll(p)
			decoded, _ := decodeBody(partBody, cte)

			if strings.HasPrefix(ct, "multipart/") {
				// 嵌套递归
				sub, htmlSub, attachSub := extractMailContent(&mail.Message{
					Header: mail.Header(p.Header),
					Body:   io.NopCloser(bytes.NewReader(decoded)),
				})
				if htmlSub != "" {
					htmlBody = htmlSub
				}
				if sub != "" && subject == "" {
					subject = sub
				}
				attachments = append(attachments, attachSub...)
				continue
			}
			if strings.HasPrefix(strings.ToLower(cd), "attachment") {
				// 附件
				_, params, _ := mime.ParseMediaType(cd)
				if filename, ok := params["filename"]; ok {
					attachments = append(attachments, filename)
				}
				continue
			}
			if strings.Contains(strings.ToLower(ct), "text/html") && htmlBody == "" {
				htmlBody = string(decoded)
			} else if strings.Contains(strings.ToLower(ct), "text/plain") && plainBody == "" {
				plainBody = "<pre>" + string(decoded) + "</pre>"
			}
		}
		if htmlBody != "" {
			return subject, htmlBody, attachments
		}
		if plainBody != "" {
			return subject, plainBody, attachments
		}
		return subject, "", attachments
	}

	// 非 multipart
	cte := msg.Header.Get("Content-Transfer-Encoding")
	decoded, _ := decodeBody(body, cte)
	if strings.Contains(strings.ToLower(mediaType), "text/html") {
		return subject, string(decoded), attachments
	}
	if strings.Contains(strings.ToLower(mediaType), "text/plain") {
		return subject, "<pre>" + string(decoded) + "</pre>", attachments
	}
	return subject, string(decoded), attachments
}

// SMTP主处理
func handleSMTP(conn net.Conn, webhookURL string, isTLS bool) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	banner := "220 smtp2wwpm ready"
	if isTLS {
		banner += " (TLS/SMTPS)"
	}
	write := func(s string) { writer.WriteString(s + "\r\n"); writer.Flush() }
	write(banner)

	var data bytes.Buffer
	stage := ""

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		rawLine := line
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		// AUTH PLAIN (兼容带参数和无参数)
		if strings.HasPrefix(upper, "AUTH PLAIN") {
			parts := strings.Fields(rawLine)
			if len(parts) == 3 {
				writer.WriteString("235 Authentication successful\r\n")
				writer.Flush()
				continue
			} else {
				writer.WriteString("334 \r\n")
				writer.Flush()
				_, _ = reader.ReadString('\n')
				writer.WriteString("235 Authentication successful\r\n")
				writer.Flush()
				continue
			}
		}

		// AUTH LOGIN (兼容带用户名和不带用户名)
		if strings.HasPrefix(upper, "AUTH LOGIN") {
			parts := strings.Fields(rawLine)
			if len(parts) == 2 {
				writer.WriteString("334 UGFzc3dvcmQ6\r\n")
				writer.Flush()
				_, _ = reader.ReadString('\n')
				writer.WriteString("235 Authentication successful\r\n")
				writer.Flush()
				continue
			} else {
				writer.WriteString("334 VXNlcm5hbWU6\r\n")
				writer.Flush()
				_, _ = reader.ReadString('\n')
				writer.WriteString("334 UGFzc3dvcmQ6\r\n")
				writer.Flush()
				_, _ = reader.ReadString('\n')
				writer.WriteString("235 Authentication successful\r\n")
				writer.Flush()
				continue
			}
		}

		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			write("250-smtp2wwpm")
			write("250-AUTH LOGIN PLAIN")
			write("250-PIPELINING")
			write("250 8BITMIME")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			write("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			write("250 OK")
		case upper == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>")
			stage = "data"
		case line == "." && stage == "data":
			go func(mailData []byte) {
				msg, err := mail.ReadMessage(bytes.NewReader(mailData))
				if err != nil {
					// 自动补全头部再试一次
					fixed := append(
						[]byte("Subject: (no subject)\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"),
						mailData...)
					msg, err = mail.ReadMessage(bytes.NewReader(fixed))
					if err != nil {
						log.Printf("[smtp2wwpm] 邮件解析失败: %v", err)
						return
					}
				}
				subject, html, attachments := extractMailContent(msg)
				log.Printf("[smtp2wwpm] subject=%q bodyLen=%d", subject, len(html))
				if len(attachments) > 0 {
					log.Printf("[smtp2wwpm] attachments=%v", attachments)
				}
				sendHTMLToWebhook(webhookURL, subject, html)
			}(data.Bytes())
			write("250 OK : queued as smtp2wwpm")
			data.Reset()
			stage = ""
		case upper == "RSET":
			data.Reset()
			stage = ""
			write("250 OK")
		case upper == "NOOP":
			write("250 OK")
		case upper == "QUIT":
			write("221 Bye")
			return
		default:
			if stage == "data" {
				data.WriteString(rawLine)
			} else {
				write("250 OK")
			}
		}
	}
}

// 生成自签TLS证书
func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"smtp2wwpm"},
			CommonName:   "localhost",
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := new(bytes.Buffer)
	keyPEM := new(bytes.Buffer)
	pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	pem.Encode(keyPEM, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return tls.X509KeyPair(certPEM.Bytes(), keyPEM.Bytes())
}

func main() {
	webhookURL := flag.String("url", "", "wecom-webhook-push-mail webhook url")
	flag.Parse()
	if *webhookURL == "" {
		fmt.Fprintf(os.Stderr, "必须指定 -url\n")
		os.Exit(1)
	}
	go func() {
		ln, err := net.Listen("tcp", ":25")
		if err != nil {
			log.Fatalf("无法监听25端口(SMTP): %v", err)
		}
		log.Printf("监听：0.0.0.0:25 (SMTP明文)")
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("25端口accept错误: %v", err)
				continue
			}
			go handleSMTP(conn, *webhookURL, false)
		}
	}()

	go func() {
		cert, err := generateSelfSignedCert()
		if err != nil {
			log.Fatalf("生成自签TLS证书失败: %v", err)
		}
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		ln, err := tls.Listen("tcp", ":465", tlsConfig)
		if err != nil {
			log.Fatalf("无法监听465端口(SMTPS): %v", err)
		}
		log.Printf("监听：0.0.0.0:465 (SMTP/TLS)")
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("465端口accept错误: %v", err)
				continue
			}
			go handleSMTP(conn, *webhookURL, true)
		}
	}()

	select {} // 阻塞主协程
}