import os
from PIL import Image, ImageDraw, ImageFilter

def draw():
    # 1. 创建 1024x1024 画布
    canvas_size = 1024
    img = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    
    # 2. 绘制 macOS Squircle 暗色高科技卡片背景
    margin = 112
    box = [margin, margin, canvas_size - margin, canvas_size - margin]
    r = 200 # 圆角半径
    
    # 创建卡片蒙版
    mask = Image.new('L', (canvas_size, canvas_size), 0)
    mask_draw = ImageDraw.Draw(mask)
    mask_draw.rounded_rectangle(box, radius=r, fill=255)
    
    card = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    # 为卡片填充更深邃的径向渐变（深黑色带一丝赛博蓝）
    for y in range(canvas_size):
        for x in range(canvas_size):
            dx = x - 512
            dy = y - 512
            dist = (dx*dx + dy*dy)**0.5
            ratio = min(1.0, dist / 600.0)
            r_c = int(10 - ratio * 6)
            g_c = int(11 - ratio * 7)
            b_c = int(18 - ratio * 12)
            card.putpixel((x, y), (r_c, g_c, b_c, 255))
            
    img = Image.composite(card, img, mask)
    draw = ImageDraw.Draw(img)
    
    # 绘制卡片边缘极细的亮色科技边框
    draw.rounded_rectangle(box, radius=r, outline=(30, 34, 52, 255), width=8)
    
    # 3. 创建火焰霓虹发光层 (Layered Glow & Flame)
    # 我们采用分层高斯模糊，以同时保留火焰的“尖锐轮廓形状”与“边缘发光效果”
    flame_layer = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    flame_draw = ImageDraw.Draw(flame_layer)
    
    # 定义更写实、尖锐的火焰形状多边形顶点 (Pointy Flame Polygons)
    # 外层大红色火焰
    red_flame = [
        (512, 110), # 尖锐的火尖
        (460, 240),
        (380, 400),
        (370, 580),
        (512, 720), # 底部中心
        (654, 580),
        (664, 400),
        (564, 240)
    ]
    # 中层橙色火焰
    orange_flame = [
        (512, 210), # 尖
        (470, 310),
        (410, 430),
        (420, 560),
        (512, 670),
        (604, 560),
        (614, 430),
        (554, 310)
    ]
    # 内层黄色明亮核心
    yellow_flame = [
        (512, 300), # 尖
        (480, 380),
        (440, 460),
        (450, 550),
        (512, 610),
        (574, 550),
        (584, 460),
        (544, 380)
    ]
    # 最内层白色高热核心
    white_core = [
        (512, 390),
        (490, 440),
        (470, 490),
        (480, 540),
        (512, 570),
        (544, 540),
        (554, 490),
        (534, 440)
    ]
    
    # 绘制各色层，并应用大小不同的模糊滤镜，创造完美的层次感火焰
    # A. 红色底层：绘制并应用大模糊 (radius=40)
    layer_red = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    draw_red = ImageDraw.Draw(layer_red)
    draw_red.polygon(red_flame, fill=(239, 68, 68, 220))
    # 飘逸的红色火星
    draw_red.ellipse([512 - 30, 80 - 30, 512 + 30, 80 + 30], fill=(239, 68, 68, 180))
    draw_red.ellipse([460 - 20, 160 - 20, 460 + 20, 160 + 20], fill=(239, 68, 68, 150))
    draw_red.ellipse([560 - 25, 140 - 25, 560 + 25, 140 + 25], fill=(239, 68, 68, 150))
    img.alpha_composite(layer_red.filter(ImageFilter.GaussianBlur(radius=40)))
    
    # B. 橙色层：绘制并应用中等模糊 (radius=18)
    layer_orange = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    draw_orange = ImageDraw.Draw(layer_orange)
    draw_orange.polygon(orange_flame, fill=(249, 115, 22, 240))
    # 橙色火星
    draw_orange.ellipse([512 - 20, 100 - 20, 512 + 20, 100 + 20], fill=(249, 115, 22, 220))
    draw_orange.ellipse([470 - 15, 190 - 15, 470 + 15, 190 + 15], fill=(249, 115, 22, 180))
    draw_orange.ellipse([550 - 18, 180 - 18, 550 + 18, 180 + 18], fill=(249, 115, 22, 180))
    img.alpha_composite(layer_orange.filter(ImageFilter.GaussianBlur(radius=18)))
    
    # C. 黄色层：轻微模糊 (radius=8)，保留火苗外廓形状
    layer_yellow = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    draw_yellow = ImageDraw.Draw(layer_yellow)
    draw_yellow.polygon(yellow_flame, fill=(234, 179, 8, 255))
    # 黄色亮点火星
    draw_yellow.ellipse([512 - 10, 120 - 10, 512 + 10, 120 + 10], fill=(255, 234, 0, 255))
    img.alpha_composite(layer_yellow.filter(ImageFilter.GaussianBlur(radius=8)))
    
    # D. 白色层：微弱模糊 (radius=3)
    layer_white = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    draw_white = ImageDraw.Draw(layer_white)
    draw_white.polygon(white_core, fill=(255, 255, 255, 255))
    img.alpha_composite(layer_white.filter(ImageFilter.GaussianBlur(radius=3)))
    
    # 4. 创建浮动硬质 Token 金币图层
    coin_layer = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    coin_draw = ImageDraw.Draw(coin_layer)
    
    cx, cy = 512, 610
    c_radius = 170
    coin_box = [cx - c_radius, cy - c_radius, cx + c_radius, cy + c_radius]
    
    # 绘制金币主体，略带透明度以透出底下火焰的红光
    coin_draw.ellipse(coin_box, fill=(8, 9, 15, 230), outline=(249, 115, 22, 255), width=10)
    
    # 金币内圈线
    inner_box = [cx - c_radius + 20, cy - c_radius + 20, cx + c_radius - 20, cy + c_radius - 20]
    coin_draw.ellipse(inner_box, outline=(249, 115, 22, 80), width=3)
    
    # 5. 绘制 3D 浮雕效果的“T”字徽标（通过橙色阴影层 + 白色上层叠加）
    # 创建 T 徽标的几何形状 (使用圆角矩形，端点圆滑，不再生硬)
    # 横梁：宽度150，高度36
    # 竖梁：宽度36，高度90
    t_shadow_layer = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    t_shadow_draw = ImageDraw.Draw(t_shadow_layer)
    
    # 绘制带 y=5 偏移的橙色霓虹发光阴影
    t_shadow_draw.rounded_rectangle([512 - 75, 610 - 55 + 5, 512 + 75, 610 - 19 + 5], radius=18, fill=(249, 115, 22, 255))
    t_shadow_draw.rounded_rectangle([512 - 18, 610 - 19 + 5, 512 + 18, 610 + 71 + 5], radius=18, fill=(249, 115, 22, 255))
    
    # 对阴影进行模糊
    img.alpha_composite(t_shadow_layer.filter(ImageFilter.GaussianBlur(radius=8)))
    
    # 绘制最上层的白净 T 字徽标，使其边角圆润美观
    coin_draw.rounded_rectangle([512 - 75, 610 - 55, 512 + 75, 610 - 19], radius=18, fill=(255, 255, 255, 255))
    coin_draw.rounded_rectangle([512 - 18, 610 - 19, 512 + 18, 610 + 71], radius=18, fill=(255, 255, 255, 255))
    
    # 绘制币面微小金色能量斑点
    coin_draw.ellipse([512 - 50, 610 - 45, 512 - 40, 610 - 35], fill=(249, 115, 22, 220))
    coin_draw.ellipse([512 + 40, 610 + 40, 512 + 50, 610 + 50], fill=(249, 115, 22, 220))
    
    # 叠加硬质金币
    img.alpha_composite(coin_layer)
    
    # 绘制金币本身的外侧边缘高光层
    coin_highlight = Image.new('RGBA', (canvas_size, canvas_size), (0, 0, 0, 0))
    ch_draw = ImageDraw.Draw(coin_highlight)
    ch_draw.ellipse(coin_box, outline=(249, 115, 22, 255), width=18)
    img.alpha_composite(coin_highlight.filter(ImageFilter.GaussianBlur(radius=12)))
    
    # 6. 保存输出
    os.makedirs('/Users/baggio/.gemini/antigravity/scratch/token_monitor', exist_ok=True)
    img.save('/Users/baggio/.gemini/antigravity/scratch/token_monitor/icon.png')
    print("[-] icon.png 3D 霓虹火焰版重新绘制成功。")

if __name__ == '__main__':
    draw()
