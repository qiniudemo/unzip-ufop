# 简介

该命令用来将上传到七牛空间中的zip文件进行解压。在某些场景下，用户需要将很多的小文件打包上传以提升上传的效率，上传完之后可以在七牛的空间中解压出一个个文件。该命令实现了zip包的解压功能，并且支持对文件名进行gbk或utf8编码的zip包。也就是说Windows下面使用自带zip工具压缩的文件可以直接上传解压。其他的场景下，可以对文件名进行utf8编码然后打包为zip文件上传，比如移动端（Android或iOS平台）。

# 命令

该命令名称为`unzip`，对应的ufop实例名称为`ufop_prefix`+`unzip`。
```
unzip/bucket/<UrlsafeBase64EncodedBucket>/prefix/<UrlsafeBase64EncodedPrefix>/overwrite/<1 or 0>
```
 
# 参数

|参数名|描述|可选|
|----------|------------|---------|
|bucket|解压到指定的空间名称|必填|
|prefix|为解压后的文件名称添加一个前缀|可选，默认为空|
|overwrite|是否覆盖空间中原有的同名文件|可选，默认为0，不覆盖|

**PS: 参数有固定的顺序，可选参数可以不设置**

**备注**：

1. `bucket`参数必须使用UrlsafeBase64编码方式编码。
2. `prefix`参数必须使用UrlsafeBase64编码方式编码。

# 配置

出于安全性的考虑，你可以根据实际的需求设置如下参数来控制unzip功能的安全性:

|Key|Value|描述|
|-------|---------|-------------|
|unzip_max_zip_file_length|默认为1GB|zip文件自身的最大大小，单位：字节，这个参数需要严格控制，以避免被恶意利用|
|unzip_max_file_length|默认为100MB|zip文件中打包的单个文件的最大大小，单位：字节，这个参数需要严格控制，以避免被恶意利用|
|unzip_max_file_count|默认为10|zip文件中打包的文件数量，这个参数需要严格控制，以避免被恶意利用|

如果需要自定义，你需要在`qufop.conf`的配置文件中添加这两项。

# 常见错误

|错误信息|描述|
|-------|------|
|invalid unzip command format|发送的ufop的指令格式不正确，请参考上面的命令格式设置正确的指令|
|invalid unzip parameter 'bucket'|指定的`bucket`参数不正确，必须是对原空间名称进行`urlsafe base64`编码后的值|
|invalid unzip parameter 'prefix'|指定的`prefix`参数不正确，必须是对原`prefix`进行`urlsafe base64`编码后的值|
|invalid unzip parameter 'overwrite'|指定的`overwrite`参数不正确，必须是`0`或者`1`|
|unsupported mimetype to unzip|需要解压的文件的类型不支持，必须是`application/zip`的才行|
|src zip file length exceeds the limit|需要解压的文件大小超过了ufop的最大允许值，这个最大允许值在`unzip.conf`里面定义|
|zip files count exceeds the limit|需要解压的文件里面的文件数量超过了ufop的最大允许值，这个最大允许值在`unzip.conf`里面定义|
|zip file length exceeds the limit|需要解压的文件里面的文件的原始大小超过了ufop的最大允许值，这个最大允许值在`unzip.conf`里面定义|

# 配置
该UFOP程序需要两个配置文件，一个是服务配置文件`qufop.conf`，其中定义了服务本事和注册UFOP服务的相关参数。另外一个是业务配置文件`unzip.conf`，里面定义了和unzip功能相关的参数。

**qufop.conf**

```
{
    "listen_port": 9100,
    "listen_host": "0.0.0.0",
    "read_timeout": 1800,
    "write_timeout": 1800,
    "max_header_bytes": 65535,
    "ufop_prefix":"qntest-"
}
```

**unzip.conf**

```
{
    "access_key": "TQt-iplt8zbK3LEHMjNYyhh6PzxkbelZFRMl10xx",
    "secret_key": "hTIq4H8N5NfCme8gDvZqr6EDmvlIQsRV5L65bVva",
    "unzip_max_zip_file_length":104857600,
    "unzip_max_file_length":104857600,
    "unzip_max_file_count":10
}
```

注意配置文件里面`ufop_prefix`和注册的ufop名称前缀一致。

# 创建

```
编译镜像 -> 本地测试 -> 上传镜像 -> 生成实例
```





# 示例

```
qntest-unzip/bucket/ZHpkcC10ZXN0
```
该指令解压出来的文件自动上传到指定空间中，所以不需要`saveas`指令。
