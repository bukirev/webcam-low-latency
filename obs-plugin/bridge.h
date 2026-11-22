#pragma once
#include <obs-module.h>

uint64_t webcam_now(void);
uint32_t webcam_module_version(void);
bool webcam_module_load(void);
void webcam_list_sender(obs_property_t *list, const char *label, const char *ip);
void webcam_video(obs_source_t *source, uint8_t *pixels, uint32_t width, uint32_t height);
void webcam_audio(obs_source_t *source, float *samples, uint32_t frames,
                  uint32_t rate, uint32_t channels, uint64_t timestamp);
