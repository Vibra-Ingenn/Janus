/**
 * llama.h — ABI reference for janus/internal/bridge
 *
 * This file is NOT compiled. It is the authoritative spec that documents
 * the exact C function signatures and struct layouts the bridge package
 * binds to via purego.
 *
 * Source: llama.cpp b5000+ (https://github.com/ggerganov/llama.cpp)
 * Target GPU: Vulkan (AMD/NVIDIA via vulkan-1.dll / libvulkan.so)
 *
 * When updating llama.cpp, re-check every struct field offset against
 * the corresponding Go struct in internal/bridge/llama_dl.go.
 */

#pragma once
#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* -------------------------------------------------------------------------
 * Opaque types — represented as uintptr in Go
 * ---------------------------------------------------------------------- */

struct llama_model;
struct llama_context;
struct llama_sampler;

typedef int32_t llama_token;
typedef int32_t llama_pos;
typedef int32_t llama_seq_id;

typedef void (*llama_progress_callback)(float progress, void * user_data);

/* -------------------------------------------------------------------------
 * Enums
 * ---------------------------------------------------------------------- */

enum llama_split_mode {
    LLAMA_SPLIT_MODE_NONE  = 0,
    LLAMA_SPLIT_MODE_LAYER = 1,
    LLAMA_SPLIT_MODE_ROW   = 2,
};

enum llama_rope_scaling_type {
    LLAMA_ROPE_SCALING_TYPE_UNSPECIFIED = -1,
    LLAMA_ROPE_SCALING_TYPE_NONE        = 0,
    LLAMA_ROPE_SCALING_TYPE_LINEAR      = 1,
    LLAMA_ROPE_SCALING_TYPE_YARN        = 2,
    LLAMA_ROPE_SCALING_TYPE_MAX_VALUE   = LLAMA_ROPE_SCALING_TYPE_YARN,
};

enum llama_pooling_type {
    LLAMA_POOLING_TYPE_UNSPECIFIED = -1,
    LLAMA_POOLING_TYPE_NONE = 0,
    LLAMA_POOLING_TYPE_MEAN = 1,
    LLAMA_POOLING_TYPE_CLS  = 2,
    LLAMA_POOLING_TYPE_LAST = 3,
};

/* -------------------------------------------------------------------------
 * llama_model_params
 *
 * Go mirror: bridge.LlamaModelParams
 * Size (64-bit): 56 bytes
 *
 * Offset  Size  Type       Name
 * 0       4     int32      n_gpu_layers
 * 4       4     int32      split_mode  (enum llama_split_mode)
 * 8       4     int32      main_gpu
 * 12      4     (padding)
 * 16      8     *float     tensor_split
 * 24      8     func ptr   progress_callback
 * 32      8     void*      progress_callback_user_data
 * 40      8     *override  kv_overrides
 * 48      1     bool       vocab_only
 * 49      1     bool       use_mmap       (default: true)
 * 50      1     bool       use_mlock
 * 51      1     bool       check_tensors
 * 52      4     (padding)
 * ---------------------------------------------------------------------- */

struct llama_model_kv_override {
    enum llama_model_kv_override_type { INT, FLOAT, BOOL, STR } tag;
    char key[128];
    union {
        int64_t  val_i64;
        double   val_f64;
        bool     val_bool;
        char     val_str[128];
    };
};

struct llama_model_params {
    const struct ggml_backend_dev ** devices; /* NULL = use all available backends */
    int32_t   n_gpu_layers;
    enum llama_split_mode split_mode;
    int32_t   main_gpu;
    const float * tensor_split;
    llama_progress_callback progress_callback;
    void *    progress_callback_user_data;
    const struct llama_model_kv_override * kv_overrides;
    bool      vocab_only;
    bool      use_mmap;
    bool      use_mlock;
    bool      check_tensors;
};

/* -------------------------------------------------------------------------
 * llama_context_params
 *
 * Go mirror: bridge.LlamaContextParams
 * Size (64-bit): 96 bytes
 *
 * Offset  Size  Type    Name
 * 0       4     uint32  n_ctx           (0 = from model)
 * 4       4     uint32  n_batch         (default 512)
 * 8       4     uint32  n_ubatch        (default 512)
 * 12      4     uint32  n_seq_max       (default 1)
 * 16      4     int32   n_threads
 * 20      4     int32   n_threads_batch
 * 24 ..   72    various rope/yarn/cb fields
 * ---------------------------------------------------------------------- */

struct llama_context_params {
    uint32_t n_ctx;
    uint32_t n_batch;
    uint32_t n_ubatch;
    uint32_t n_seq_max;
    int32_t  n_threads;
    int32_t  n_threads_batch;
    enum llama_rope_scaling_type rope_scaling_type;
    enum llama_pooling_type      pooling_type;
    float    rope_freq_base;
    float    rope_freq_scale;
    float    yarn_ext_factor;
    float    yarn_attn_factor;
    float    yarn_beta_fast;
    float    yarn_beta_slow;
    uint32_t yarn_orig_ctx;
    float    defrag_thold;
    void *   cb_eval;
    void *   cb_eval_user_data;
    int32_t  type_k;   /* ggml_type */
    int32_t  type_v;   /* ggml_type */
    bool     logits_all;
    bool     embeddings;
    bool     offload_kqv;
    bool     flash_attn;
    bool     no_perf;
    void *   abort_callback;
    void *   abort_callback_data;
};

/* -------------------------------------------------------------------------
 * llama_batch
 *
 * Go mirror: bridge.LlamaBatch
 * Size (64-bit): 72 bytes
 *
 * Offset  Size  Type          Name
 * 0       4     int32         n_tokens
 * 4       4     (padding)
 * 8       8     *llama_token  token
 * 16      8     *float        embd     (NULL for token mode)
 * 24      8     *llama_pos    pos
 * 32      8     *int32        n_seq_id
 * 40      8     **llama_seq_id seq_id
 * 48      8     *int8         logits
 * 56      4     llama_pos     all_pos_0
 * 60      4     llama_pos     all_pos_1
 * 64      4     llama_seq_id  all_seq_id
 * 68      4     (padding)
 * ---------------------------------------------------------------------- */

struct llama_batch {
    int32_t          n_tokens;
    llama_token    * token;
    float          * embd;
    llama_pos      * pos;
    int32_t        * n_seq_id;
    llama_seq_id  ** seq_id;
    int8_t         * logits;
    llama_pos        all_pos_0;
    llama_pos        all_pos_1;
    llama_seq_id     all_seq_id;
};

/* -------------------------------------------------------------------------
 * Core API — the 8 symbols registered in bridge.LlamaLib
 * ---------------------------------------------------------------------- */

/* Backend lifecycle (call once at startup/shutdown) */
void llama_backend_init (void);
void llama_backend_free (void);

/* Model lifecycle */
struct llama_model * llama_load_model_from_file (
    const char * path_model,
    struct llama_model_params params
);
void llama_free_model (struct llama_model * model);

/* Context lifecycle */
struct llama_context * llama_new_context_with_model (
    struct llama_model          * model,
    struct llama_context_params   params
);
void llama_free (struct llama_context * ctx);

/* Tokenize */
int32_t llama_tokenize (
    const struct llama_model * model,
    const char               * text,
    int32_t                    text_len,
    llama_token              * tokens,
    int32_t                    n_tokens_max,
    bool                       add_special,
    bool                       parse_special
);

/* Decode (forward pass) */
int llama_decode (struct llama_context * ctx, struct llama_batch batch);

/* -------------------------------------------------------------------------
 * Additional symbols needed for the token generation loop (future)
 * Add these to bridge.LlamaLib when implementing Generate().
 * ---------------------------------------------------------------------- */

/* Logits — call after llama_decode to read output distribution */
float * llama_get_logits      (struct llama_context * ctx);
float * llama_get_logits_ith  (struct llama_context * ctx, int32_t i);

/* Greedy sampler (simplest — no temperature/top-p) */
llama_token llama_sample_token_greedy (
    struct llama_context * ctx,
    struct llama_token_data_array * candidates
);

/* Detokenize a single token to a UTF-8 string fragment */
int32_t llama_token_to_piece (
    const struct llama_model * model,
    llama_token                token,
    char                     * buf,
    int32_t                    length,
    int32_t                    lstrip,
    bool                       special
);

/* EOS / BOS token IDs */
llama_token llama_token_eos (const struct llama_model * model);
llama_token llama_token_bos (const struct llama_model * model);

/* KV cache management */
void llama_kv_cache_clear     (struct llama_context * ctx);
void llama_kv_cache_seq_rm    (struct llama_context * ctx,
                               llama_seq_id seq_id,
                               llama_pos    p0,
                               llama_pos    p1);

#ifdef __cplusplus
}
#endif
