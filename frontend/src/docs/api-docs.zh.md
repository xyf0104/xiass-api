## API 接入文档

欢迎使用我们的 API 服务！我们的 API 完全兼容标准的 OpenAI 接口格式，你可以使用任何支持 OpenAI 的客户端（如 Python/NodeJS SDK，或各种开源客户端软件）来无缝接入。

---

### 1. 基础配置

要连接到我们的服务，你只需要在你的客户端中配置以下两个核心参数：

- **API Base URL (接口地址)**: `https://api.你的域名.com/v1` （请根据你的实际网关地址替换，确保保留 `/v1` 后缀）
- **API Key (接口密钥)**: 在左侧菜单【API 密钥】中创建并获取。

---

### 2. Python 接入示例

推荐使用官方的 `openai` 库接入。首先安装依赖：
```bash
pip install openai
```

然后使用以下代码调用大模型：
```python
from openai import OpenAI

# 替换为你的 API Key 和 我们的 Base URL
client = OpenAI(
    api_key="sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    base_url="https://api.你的域名.com/v1"
)

response = client.chat.completions.create(
    model="claude-3-5-sonnet-20240620", # 此处替换为你需要的模型名称
    messages=[
        {"role": "system", "content": "你是一个非常有用的AI助手。"},
        {"role": "user", "content": "你好，请自我介绍一下。"}
    ]
)

print(response.choices[0].message.content)
```

---

### 3. Node.js 接入示例

使用 Node.js 官方 `openai` 库：
```bash
npm install openai
```

代码示例：
```javascript
import OpenAI from 'openai';

const openai = new OpenAI({
  apiKey: 'sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx',
  baseURL: 'https://api.你的域名.com/v1'
});

async function main() {
  const chatCompletion = await openai.chat.completions.create({
    messages: [{ role: 'user', content: '你好，请介绍一下你自己。' }],
    model: 'claude-3-5-sonnet-20240620',
  });
  
  console.log(chatCompletion.choices[0].message.content);
}

main();
```

---

### 4. 常见第三方客户端接入指南

如果你使用的是第三方软件（如 ChatNextWeb, LobeChat, BotGem 等）：
1. 找到软件的**设置** -> **模型服务商** -> 选择 **OpenAI** (或自定义)。
2. 将 **接口地址 (Base URL)** 修改为：`https://api.你的域名.com` （部分软件不需要加 `/v1`，请根据软件提示输入）。
3. 将 **API Key** 填入你的专属密钥。
4. 在模型列表中输入并选择你想使用的模型名称即可开始对话！

> [!TIP]
> **计费说明**
> 所有的接口计费都是实时从你的账户余额中扣除的。请确保你的账户中有充足的余额。你可以在左侧【模型价格】查看所有支持的模型及其实时计费标准。
