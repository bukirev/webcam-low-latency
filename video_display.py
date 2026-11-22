# Переиспользование PhotoImage для Python/Tk: show вызывается при новом изображении,
# а не на каждом poll UI. Это снижает стоимость выделений, но остаётся CPU/Tk-путём.
# Сравнение с GPU: docs/modules/render.md и docs/lessons-learned.md.
"""Reuse Tk image storage instead of allocating a new photo for every frame."""
import cv2
from PIL import Image, ImageTk


# Только Tk UI thread. Двойной PhotoImage cache позволяет paste в непоказанную
# картинку, затем переключить widget, сохранив ссылку от сборки мусора.
class FramePresenter:
    def __init__(self, widget):
        self.widget = widget
        self.photos = []
        self.size = None
        self.current = 0

    def show(self, frame):
        image = Image.fromarray(cv2.cvtColor(frame, cv2.COLOR_BGR2RGB))
        if image.size != self.size:
            self.photos = []
            self.size = image.size
        if len(self.photos) < 2:
            photo = ImageTk.PhotoImage(image, master=self.widget)
            self.photos.append(photo)
            self.current = len(self.photos) - 1
        else:
            self.current = 1 - self.current
            photo = self.photos[self.current]
            # The target photo is not displayed. Pasting into the visible Tk
            # photo can trigger a much more expensive redraw inside Pillow/Tk.
            photo.paste(image)
        self.widget.configure(image=photo, text='')
        self.widget.image = photo

    def clear(self, text=''):
        self.widget.configure(image='', text=text)
        self.widget.image = None
        self.photos = []
        self.size = None
