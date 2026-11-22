param([string]$Output = (Join-Path $PSScriptRoot '../go-version/internal/brand/assets'))
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
New-Item -ItemType Directory -Path $Output -Force | Out-Null

# Two open links from the supplied wordmark. Build the Windows icon at each
# size directly so its strokes and silhouette stay readable in the taskbar.
function New-IconImage([int]$Size) {
    $bitmap = [Drawing.Bitmap]::new($Size, $Size, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [Drawing.Graphics]::FromImage($bitmap)
    try {
        $g.SmoothingMode = [Drawing.Drawing2D.SmoothingMode]::AntiAlias
        $g.ScaleTransform($Size / 256.0, $Size / 256.0)
        $tile = [Drawing.Drawing2D.GraphicsPath]::new()
        foreach ($corner in @(@(10,10,180),@(180,10,270),@(180,180,0),@(10,180,90))) {
            $tile.AddArc($corner[0],$corner[1],66,66,$corner[2],90)
        }
        $tile.CloseFigure()
        $background = [Drawing.Drawing2D.LinearGradientBrush]::new(
            [Drawing.Rectangle]::new(0,0,256,256),
            [Drawing.Color]::FromArgb(18,49,82),[Drawing.Color]::FromArgb(3,17,39),90)
        $edge = [Drawing.Pen]::new([Drawing.Color]::FromArgb(52,102,145),5)
        $g.FillPath($background,$tile)
        $g.DrawPath($edge,$tile)
        $edge.Dispose()
        $background.Dispose()
        $tile.Dispose()

        $rect = [Drawing.Rectangle]::new(30,50,200,156)
        foreach ($isBlue in @($false,$true)) {
            $path = [Drawing.Drawing2D.GraphicsPath]::new()
            if ($isBlue) {
                $path.AddLine(136,113,88,113)
                $path.AddBezier(88,113,60,113,49,129,49,150)
                $path.AddBezier(49,150,49,176,66,189,90,189)
                $path.AddLine(90,189,134,189)
                $top = [Drawing.Color]::FromArgb(45,238,255)
                $bottom = [Drawing.Color]::FromArgb(0,119,255)
            } else {
                $path.AddLine(121,67,167,67)
                $path.AddBezier(167,67,193,67,207,83,207,105)
                $path.AddBezier(207,105,207,129,192,143,166,143)
                $path.AddLine(166,143,119,143)
                $top = [Drawing.Color]::FromArgb(255,228,90)
                $bottom = [Drawing.Color]::FromArgb(255,163,0)
            }
            $gradient = [Drawing.Drawing2D.LinearGradientBrush]::new($rect,$top,$bottom,90)
            $pen = [Drawing.Pen]::new($gradient,27)
            $pen.StartCap = $pen.EndCap = [Drawing.Drawing2D.LineCap]::Round
            $pen.LineJoin = [Drawing.Drawing2D.LineJoin]::Round
            $g.DrawPath($pen,$path)
            $pen.Dispose()
            $gradient.Dispose()
            $path.Dispose()
        }
    } finally { $g.Dispose() }
    return $bitmap
}

$sizes = @(16,24,32,48,64,128,256)
$images = @{}
foreach ($size in $sizes) {
    $bitmap = New-IconImage $size
    $buffer = [IO.MemoryStream]::new()
    try {
        $bitmap.Save($buffer,[Drawing.Imaging.ImageFormat]::Png)
        $images[$size] = $buffer.ToArray()
        if ($size -eq 128) { [IO.File]::WriteAllBytes((Join-Path $Output 'mark.png'),$images[$size]) }
    } finally { $bitmap.Dispose(); $buffer.Dispose() }
}
$stream = [IO.File]::Create((Join-Path $Output 'VB2Link.ico'))
$writer = [IO.BinaryWriter]::new($stream)
try {
    $writer.Write([uint16]0)
    $writer.Write([uint16]1)
    $writer.Write([uint16]$sizes.Count)
    $offset = 6 + 16*$sizes.Count
    foreach ($size in $sizes) {
        $writer.Write([byte]($size % 256))
        $writer.Write([byte]($size % 256))
        $writer.Write([uint16]0)
        $writer.Write([uint16]1)
        $writer.Write([uint16]32)
        $writer.Write([uint32]$images[$size].Length)
        $writer.Write([uint32]$offset)
        $offset += $images[$size].Length
    }
    foreach ($size in $sizes) { $writer.Write([byte[]]$images[$size]) }
} finally { $writer.Dispose() }
