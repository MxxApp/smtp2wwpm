# smtp2wwpm

本项目需配合 [wwpm](https://github.com/MxxApp/wecom-webhook-push-mail) 使用，单独无法使用。

用于云服务器无法直接发信（SMTP端口被封）时，在本机伪造 SMTP 服务，捕获邮件内容并通过 HTTP 转发到 [wwpm](https://github.com/MxxApp/wecom-webhook-push-mail) 实现最终邮件发送。

你至少需要有一台SMTP端口开放的服务器，用来搭建 [wwpm](https://github.com/MxxApp/wecom-webhook-push-mail)。

解决搭建某些服务，只支持SMTP推送通知，服务器却不开放SMTP端口。

---

## 特性

- 本地伪造25和465端口，捕获SMTP邮件内容
- 自动转发邮件内容到远程 HTTP 邮件中转服务

---

## 安装和使用

### 1. 下载二进制

- 前往 [Releases 页面](https://github.com/MxxApp/smtp2wwpm/releases) 下载对应架构的二进制包，解压得到 stmp2wwpm。

### 2. 启动服务

只需指定 webhook 地址（即你的 wecom-webhook-push-mail 推送接口）：

```sh
./smtp2wwpm -url=http://your-wecom-webhook-server:8080/cgi-bin/webhook/send?key=xxxxxx
```

### 3. 业务配置

将你的业务系统的 SMTP 服务器地址指向本机 25 或 465 端口即可，用户名/密码随便写。

---

## Docker 部署

1. **拉取镜像**
    ```sh
    docker pull mxxapp/smtp2wwpm:latest
    ```

2. **运行容器**
    ```sh
    docker run -d \
      --name smtp2wwpm \
      -p 25:25 -p 465:465 \
      mxxapp/smtp2wwpm:latest \
      -url=http://your-wecom-webhook-server:8080/cgi-bin/webhook/send?key=xxxxxx
    ```

---

## 附件处理说明

- 邮件中的附件会被自动识别，并在日志中打印文件名，例如：

  `[ATTACH] subject="邮件标题" attachments=["foo.pdf", "bar.zip"]`
- 附件内容不会上传、转发或保存。

---

## 依赖项目

- [wecom-webhook-push-mail](https://github.com/MxxApp/wecom-webhook-push-mail)

---

## 常见问题

- **无法监听25或465端口？**
    - 需 root 权限
    - 检查是否被其它程序占用
    ```sh
    sudo netstat -tlnp | grep :25 && sudo netstat -tlnp | grep :465
    ```
    - 或通过端口映射（docker -p）
- **转发失败？**
    - 检查 webhook 地址是否可访问，wecom-webhook-push-mail 服务是否正常。
