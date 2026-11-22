param(
    [ValidateRange(1, 65535)][int]$Port = 4455,
    [System.Security.SecureString]$Password
)
$ErrorActionPreference = 'Stop'
$socket = [Net.WebSockets.ClientWebSocket]::new()
$utf8 = [Text.Encoding]::UTF8

function Send-OBS($Message) {
    $bytes = $utf8.GetBytes(($Message | ConvertTo-Json -Depth 12 -Compress))
    $cancel = [Threading.CancellationTokenSource]::new(5000)
    try {
        $null = $socket.SendAsync([ArraySegment[byte]]::new($bytes), 'Text', $true, $cancel.Token).GetAwaiter().GetResult()
    } finally { $cancel.Dispose() }
}

function Receive-OBS {
    $bytes = [byte[]]::new(8192)
    $message = [IO.MemoryStream]::new()
    $cancel = [Threading.CancellationTokenSource]::new(5000)
    try {
        do {
            $part = $socket.ReceiveAsync([ArraySegment[byte]]::new($bytes), $cancel.Token).GetAwaiter().GetResult()
            if ($part.MessageType -ne 'Text') { throw 'OBS закрыл соединение или прислал сообщение неизвестного типа. Проверьте пароль.' }
            $message.Write($bytes, 0, $part.Count)
            if ($message.Length -gt 4194304) { throw 'Ответ OBS превышает 4 МБ.' }
        } until ($part.EndOfMessage)
        $utf8.GetString($message.ToArray()) | ConvertFrom-Json
    } finally {
        $cancel.Dispose()
        $message.Dispose()
    }
}

function Invoke-OBS([string]$Type, [hashtable]$Data = @{}) {
    $id = [guid]::NewGuid().ToString()
    Send-OBS @{op = 6; d = @{requestType = $Type; requestId = $id; requestData = $Data}}
    do { $reply = Receive-OBS } until ($reply.op -eq 7 -and $reply.d.requestId -eq $id)
    $status = $reply.d.requestStatus
    if (-not $status.result) { throw "$Type ($($status.code)): $($status.comment)" }
    $reply.d.responseData
}

function Get-HashBase64([string]$Value) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try { [Convert]::ToBase64String($sha.ComputeHash($utf8.GetBytes($Value))) }
    finally { $sha.Dispose() }
}

$feeds = @{
    Camera = @{Name = 'VB2Link · Камера'; Address = '127.0.0.1:16601'}
    Screen = @{Name = 'VB2Link · Экран'; Address = '127.0.0.1:16602'}
    Audio  = @{Name = 'VB2Link · Звук'; Address = '127.0.0.1:16603'}
}
$layouts = @(
    @{Name = 'VB2Link — Камера'; Feeds = @('Camera', 'Audio')},
    @{Name = 'VB2Link — Экран'; Feeds = @('Screen', 'Audio')},
    @{Name = 'VB2Link — Экран + камера'; Feeds = @('Screen', 'Camera', 'Audio')}
)

try {
    $cancel = [Threading.CancellationTokenSource]::new(5000)
    try {
        $null = $socket.ConnectAsync([uri]"ws://127.0.0.1:$Port", $cancel.Token).GetAwaiter().GetResult()
    } catch {
        throw "Нет подключения к OBS на 127.0.0.1:$Port. Запустите OBS и включите сервер WebSocket в меню «Инструменты»."
    } finally { $cancel.Dispose() }
    $hello = Receive-OBS
    if ($hello.op -ne 0 -or $hello.d.rpcVersion -lt 1) { throw 'Требуется OBS WebSocket 5.' }
    $identify = @{rpcVersion = 1; eventSubscriptions = 0}
    if ($hello.d.authentication) {
        if ($null -eq $Password) { $Password = Read-Host 'Пароль WebSocket из настроек OBS' -AsSecureString }
        $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Password)
        try {
            $secret = Get-HashBase64 (([Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)) + $hello.d.authentication.salt)
            $identify.authentication = Get-HashBase64 ($secret + $hello.d.authentication.challenge)
        } finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr) }
    }
    Send-OBS @{op = 1; d = $identify}
    if ((Receive-OBS).op -ne 2) { throw 'OBS не подтвердил подключение. Проверьте пароль.' }

    $kind = 'teleport-source'
    if ((Invoke-OBS GetInputKindList).inputKinds -notcontains $kind) {
        throw 'Плагин Teleport не загружен. Установите совместимую версию и перезапустите OBS.'
    }
    $inputs = @{}
    foreach ($inputInfo in (Invoke-OBS GetInputList).inputs) { $inputs[$inputInfo.inputName] = $inputInfo.inputKind }
    $scenes = (Invoke-OBS GetSceneList).scenes.sceneName
    $canvas = Invoke-OBS GetVideoSettings

    # Existing collections may already use the names from the previous bundle.
    foreach ($feed in $feeds.Values) {
        $old = $feed.Name.Replace('VB2Link', 'WebcamGo')
        if (-not $inputs.ContainsKey($feed.Name) -and $inputs[$old] -eq $kind) { $feed.Name = $old }
    }
    foreach ($layout in $layouts) {
        $old = $layout.Name.Replace('VB2Link', 'WebcamGo')
        if ($scenes -notcontains $layout.Name -and $scenes -contains $old) { $layout.Name = $old }
    }

    # Check all reused names before creating anything. Preserve the user's input settings.
    foreach ($feed in $feeds.Values) {
        if ($scenes -contains $feed.Name) { throw "Имя «$($feed.Name)» уже занято сценой." }
        if (-not $inputs.ContainsKey($feed.Name)) { continue }
        if ($inputs[$feed.Name] -ne $kind) { throw "Имя «$($feed.Name)» уже занято источником другого типа." }
        $settings = (Invoke-OBS GetInputSettings @{inputName = $feed.Name}).inputSettings
        if ($settings.'manual-ip' -ne $feed.Address -or $settings.'has-audio-and-video') {
            throw "У источника «$($feed.Name)» другой адрес или включён Video And Audio. Проверьте его свойства в OBS."
        }
    }
    foreach ($layout in $layouts) {
        if ($inputs.ContainsKey($layout.Name)) { throw "Имя «$($layout.Name)» уже занято источником." }
    }

    foreach ($layout in $layouts) {
        $sceneName = $layout.Name
        if ($scenes -notcontains $sceneName) {
            Invoke-OBS CreateScene @{sceneName = $sceneName} | Out-Null
        }
        $items = (Invoke-OBS GetSceneItemList @{sceneName = $sceneName}).sceneItems
        foreach ($role in $layout.Feeds) {
            $feed = $feeds[$role]
            if ($items.sourceName -contains $feed.Name) { continue }
            if ($inputs.ContainsKey($feed.Name)) {
                $created = Invoke-OBS CreateSceneItem @{sceneName = $sceneName; sourceName = $feed.Name; sceneItemEnabled = $true}
            } else {
                $created = Invoke-OBS CreateInput @{
                    sceneName = $sceneName; inputName = $feed.Name; inputKind = $kind
                    inputSettings = @{advanced = $true; 'manual-ip' = $feed.Address; 'has-audio-and-video' = $false}
                    sceneItemEnabled = $true
                }
                $inputs[$feed.Name] = $kind
            }
            if ($role -eq 'Audio') { continue }
            if ($role -eq 'Screen' -and $layout.Feeds -contains 'Camera') {
                Invoke-OBS SetSceneItemIndex @{sceneName = $sceneName; sceneItemId = $created.sceneItemId; sceneItemIndex = 0} | Out-Null
            }
            $width = $canvas.baseWidth
            $height = $canvas.baseHeight
            $left = 0
            $top = 0
            if ($role -eq 'Camera' -and $layout.Feeds -contains 'Screen') {
                $width = [Math]::Round($width * 0.25)
                $height = [Math]::Round($height * 0.25)
                $left = [Math]::Round($canvas.baseWidth * 0.98) - $width
                $top = [Math]::Round($canvas.baseHeight * 0.98) - $height
            }
            Invoke-OBS SetSceneItemTransform @{
                sceneName = $sceneName; sceneItemId = $created.sceneItemId
                sceneItemTransform = @{
                    positionX = $left; positionY = $top; alignment = 5
                    boundsType = 'OBS_BOUNDS_SCALE_INNER'; boundsAlignment = 0
                    boundsWidth = $width; boundsHeight = $height
                }
            } | Out-Null
        }
        Write-Host "Готово: $sceneName"
    }
} catch {
    Write-Error -ErrorAction Continue $_.Exception.Message
    exit 1
} finally {
    $socket.Dispose()
}
