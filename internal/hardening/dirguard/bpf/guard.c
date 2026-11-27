// SPDX-License-Identifier: GPL-2.0
#include "vmlinux.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#define ROOTS 32
#define STATES 512
#define PATHSIZE 4096
struct record {
	__u32 length, start, accept, count;
	__u64 device, inode;
	__u32 initial, padding;
	char root[1024];
	__u16 next[256];
};
struct policy_map {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, ROOTS *(STATES + 1) + 1);
	__uint(map_flags, 1);
	__type(key, __u32);
	__type(value, struct record);
};
struct policy_map template SEC(".maps");
struct {
	__uint(type, BPF_MAP_TYPE_ARRAY_OF_MAPS);
	__uint(max_entries, 1);
	__type(key, __u32);
	__array(values, struct policy_map);
} policies_active SEC(".maps");
struct buffer {
	char path[PATHSIZE];
	char name[256];
};
struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct buffer);
} scratch SEC(".maps");

struct match_ctx {
	void *policies;
	char *path;
	__u32 offset, length, start, state;
	int error;
};
static long transition(__u32 p, void *data)
{
	struct match_ctx *ctx = data;
	__u32 index = p + ctx->offset;
	if (index >= ctx->length)
		return 1;
	if (index >= PATHSIZE) {
		ctx->error = 1;
		return 1;
	}
	__u32 key = ctx->start + ctx->state;
	struct record *row = bpf_map_lookup_elem(ctx->policies, &key);
	if (!row) {
		ctx->error = 1;
		return 1;
	}
	__u8 c = ctx->path[index];
	ctx->state = row->next[c];
	if (ctx->state >= STATES) {
		ctx->error = 1;
		return 1;
	}
	return 0;
}
struct prefix_ctx {
	char *path;
	struct record *rule;
	__u32 length;
	int equal;
};
static long compare_prefix(__u32 p, void *data)
{
	struct prefix_ctx *ctx = data;
	if (p >= ctx->length)
		return 1;
	if (p >= 1024) {
		ctx->equal = 0;
		return 1;
	}
	if (ctx->path[p] != ctx->rule->root[p]) {
		ctx->equal = 0;
		return 1;
	}
	return 0;
}
struct rules_ctx {
	void *policies;
	char *path;
	__u32 length, count, ancestors;
	int denied;
};
static long check_rule(__u32 n, void *data)
{
	struct rules_ctx *ctx = data;
	if (n >= ctx->count)
		return 1;
	__u32 key = n + 1;
	struct record *rule = bpf_map_lookup_elem(ctx->policies, &key);
	if (!rule)
		return 0;
	__u32 rootlen = rule->length;
	if (!rootlen || rootlen >= 1024)
		return 0;
	if (rootlen > ctx->length && !ctx->ancestors)
		return 0;
	__u32 comparelen = rootlen < ctx->length ? rootlen : ctx->length;
	struct prefix_ctx prefix = {
	    .path = ctx->path, .rule = rule, .length = comparelen, .equal = 1};
	bpf_loop(1024, compare_prefix, &prefix, 0);
	if (!prefix.equal)
		return 0;
	if (rootlen == ctx->length) {
		ctx->denied = 1;
		return 1;
	}
	if (rootlen > ctx->length) {
		if (ctx->length < 1024 && rule->root[ctx->length] == '/') {
			ctx->denied = 1;
			return 1;
		}
		return 0;
	}
	if (ctx->path[rootlen] != '/')
		return 0;
	struct match_ctx match = {.policies = ctx->policies,
				  .path = ctx->path,
				  .offset = rootlen + 1,
				  .length = ctx->length,
				  .start = rule->start};
	bpf_loop(PATHSIZE, transition, &match, 0);
	if (match.error) {
		ctx->denied = 1;
		return 1;
	}
	__u32 endkey = match.start + match.state;
	struct record *end = bpf_map_lookup_elem(ctx->policies, &endkey);
	if (!end || !end->accept) {
		ctx->denied = 1;
		return 1;
	}
	return 0;
}
static __noinline int denied(char *path, int length, int ancestors)
{
	__u32 zero = 0;
	void *policies = bpf_map_lookup_elem(&policies_active, &zero);
	if (!policies)
		return 0;
	struct record *meta = bpf_map_lookup_elem(policies, &zero);
	if (!meta)
		return 0;
	if (meta->count > ROOTS)
		return -13;
	struct rules_ctx ctx = {.policies = policies,
				.path = path,
				.length = length,
				.count = meta->count,
				.ancestors = ancestors};
	bpf_loop(ROOTS, check_rule, &ctx, 0);
	return ctx.denied ? -13 : 0;
}

static __always_inline int check(const struct path *path, struct dentry *child, int ancestors)
{
	__u32 zero = 0;
	void *policies = bpf_map_lookup_elem(&policies_active, &zero);
	if (!policies)
		return 0;
	struct record *meta = bpf_map_lookup_elem(policies, &zero);
	if (!meta || !meta->count)
		return 0;
	struct buffer *buf = bpf_map_lookup_elem(&scratch, &zero);
	if (!buf)
		return -13;
	int length = bpf_path_d_path(path, buf->path, PATHSIZE);
	if (length <= 0 || length > PATHSIZE)
		return -13;
	if (child) {
		__u32 offset = length - 1;
		if (offset >= PATHSIZE - 1)
			return -13;
		if (offset > 1) {
			buf->path[offset] = '/';
			offset++;
		}
		const unsigned char *name = BPF_CORE_READ(child, d_name.name);
		int used = bpf_probe_read_kernel_str(buf->name, 256, name);
		if (used <= 0 || used > 256)
			return -13;
		for (__u32 n = 0; n < 256; n++) {
			if (n >= used)
				break;
			__u32 index = offset + n;
			if (index >= PATHSIZE)
				return -13;
			buf->path[index] = buf->name[n];
		}
		length = offset + used;
	}
	return denied(buf->path, length - 1, ancestors);
}
// 硬链接源没有 mount 路径参数，按已解析目录的设备/inode 锚点回溯真实相对路径。
struct walk_ctx {
	struct dentry *cursor;
	struct record *rule;
	struct buffer *buf;
	__u32 offset;
	int found, error;
};
static long walk_source(__u32 n, void *data)
{
	struct walk_ctx *ctx = data;
	__u32 offset = ctx->offset;
	if (offset <= 1 || offset >= PATHSIZE) {
		ctx->error = 1;
		return 1;
	}
	struct inode *inode = BPF_CORE_READ(ctx->cursor, d_inode);
	if (!inode) {
		ctx->error = 1;
		return 1;
	}
	__u64 ino = BPF_CORE_READ(inode, i_ino);
	__u64 dev = BPF_CORE_READ(inode, i_sb, s_dev);
	if (ino == ctx->rule->inode && dev == ctx->rule->device) {
		ctx->found = 1;
		return 1;
	}
	struct dentry *parent = BPF_CORE_READ(ctx->cursor, d_parent);
	if (!parent || parent == ctx->cursor)
		return 1;
	const unsigned char *name = BPF_CORE_READ(ctx->cursor, d_name.name);
	int used = bpf_probe_read_kernel_str(ctx->buf->name, 256, name);
	if (used <= 1 || used > 256) {
		ctx->error = 1;
		return 1;
	}
	__u32 count = used - 1;
	if (count + 1 >= offset) {
		ctx->error = 1;
		return 1;
	}
	if (offset < PATHSIZE - 1) {
		offset--;
		if (offset >= PATHSIZE) {
			ctx->error = 1;
			return 1;
		}
		ctx->buf->path[offset] = '/';
	}
	offset -= count;
	ctx->offset = offset;
	for (__u32 p = 0; p < 256; p++) {
		if (p >= count)
			break;
		__u32 index = offset + p;
		if (index >= PATHSIZE) {
			ctx->error = 1;
			return 1;
		}
		ctx->buf->path[index] = ctx->buf->name[p];
	}
	ctx->cursor = parent;
	return 0;
}
struct source_ctx {
	void *policies;
	struct dentry *source;
	struct buffer *buf;
	__u32 count;
	int denied;
};
static long check_source_rule(__u32 n, void *data)
{
	struct source_ctx *ctx = data;
	if (n >= ctx->count)
		return 1;
	__u32 key = n + 1;
	struct record *rule = bpf_map_lookup_elem(ctx->policies, &key);
	if (!rule)
		return 0;
	struct inode *inode = BPF_CORE_READ(ctx->source, d_inode);
	if (!inode)
		return 0;
	__u64 dev = BPF_CORE_READ(inode, i_sb, s_dev);
	if (dev != rule->device)
		return 0;
	struct walk_ctx walk = {
	    .cursor = ctx->source, .rule = rule, .buf = ctx->buf, .offset = PATHSIZE - 1};
	bpf_loop(PATHSIZE / 2, walk_source, &walk, 0);
	if (walk.error) {
		ctx->denied = 1;
		return 1;
	}
	if (!walk.found)
		return 0;
	struct match_ctx match = {.policies = ctx->policies,
				  .path = ctx->buf->path,
				  .offset = walk.offset,
				  .length = PATHSIZE - 1,
				  .start = rule->start,
				  .state = rule->initial};
	bpf_loop(PATHSIZE, transition, &match, 0);
	if (match.error) {
		ctx->denied = 1;
		return 1;
	}
	__u32 endkey = match.start + match.state;
	struct record *end = bpf_map_lookup_elem(ctx->policies, &endkey);
	if (!end || !end->accept) {
		ctx->denied = 1;
		return 1;
	}
	return 0;
}
static __always_inline int check_source(struct dentry *source)
{
	__u32 zero = 0;
	void *policies = bpf_map_lookup_elem(&policies_active, &zero);
	if (!policies)
		return 0;
	struct record *meta = bpf_map_lookup_elem(policies, &zero);
	if (!meta || !meta->count)
		return 0;
	struct buffer *buf = bpf_map_lookup_elem(&scratch, &zero);
	if (!buf || meta->count > ROOTS)
		return -13;
	struct source_ctx ctx = {
	    .policies = policies, .source = source, .buf = buf, .count = meta->count};
	bpf_loop(ROOTS, check_source_rule, &ctx, 0);
	return ctx.denied ? -13 : 0;
}
SEC("lsm/path_mknod")
int BPF_PROG(dir_mknod, const struct path *dir, struct dentry *dentry, umode_t mode,
	     unsigned int dev, int ret)
{
	return ret ? ret : check(dir, dentry, 0);
}
SEC("lsm/path_mkdir")
int BPF_PROG(dir_mkdir, const struct path *dir, struct dentry *dentry, umode_t mode, int ret)
{
	return ret ? ret : check(dir, dentry, 0);
}
SEC("lsm/path_unlink")
int BPF_PROG(dir_unlink, const struct path *dir, struct dentry *dentry, int ret)
{
	return ret ? ret : check(dir, dentry, 0);
}
SEC("lsm/path_rmdir")
int BPF_PROG(dir_rmdir, const struct path *dir, struct dentry *dentry, int ret)
{
	return ret ? ret : check(dir, dentry, 0);
}
SEC("lsm/path_symlink")
int BPF_PROG(dir_symlink, const struct path *dir, struct dentry *dentry, const char *target,
	     int ret)
{
	return ret ? ret : check(dir, dentry, 0);
}
SEC("lsm/path_link")
int BPF_PROG(dir_link, struct dentry *source, const struct path *dir, struct dentry *dentry,
	     int ret)
{
	if (ret)
		return ret;
	int error = check_source(source);
	return error ? error : check(dir, dentry, 0);
}
SEC("lsm/path_rename")
int BPF_PROG(dir_rename, const struct path *old_dir, struct dentry *old_entry,
	     const struct path *new_dir, struct dentry *new_entry, unsigned int flags, int ret)
{
	if (ret)
		return ret;
	int e = check(old_dir, old_entry, 1);
	return e ? e : check(new_dir, new_entry, 1);
}
SEC("lsm/path_truncate") int BPF_PROG(dir_truncate, const struct path *path, int ret)
{
	return ret ? ret : check(path, 0, 0);
}
SEC("lsm/path_chmod") int BPF_PROG(dir_chmod, const struct path *path, umode_t mode, int ret)
{
	return ret ? ret : check(path, 0, 0);
}
SEC("lsm/path_chown")
int BPF_PROG(dir_chown, const struct path *path, unsigned int uid, unsigned int gid, int ret)
{
	return ret ? ret : check(path, 0, 0);
}
SEC("lsm/file_open") int BPF_PROG(dir_open, struct file *file, int ret)
{
	if (ret || !(file->f_mode & 2))
		return ret;
	return check(&file->f_path, 0, 0);
}
SEC("lsm/file_truncate") int BPF_PROG(dir_file_truncate, struct file *file, int ret)
{
	return ret ? ret : check(&file->f_path, 0, 0);
}
SEC("lsm/file_permission") int BPF_PROG(dir_permission, struct file *file, int mask, int ret)
{
	if (ret || !(mask & 2))
		return ret;
	return check(&file->f_path, 0, 0);
}
SEC("lsm/mmap_file")
int BPF_PROG(dir_mmap, struct file *file, unsigned long reqprot, unsigned long prot,
	     unsigned long flags, int ret)
{
	if (ret || !file || !(prot & 2) || !(flags & 1))
		return ret;
	return check(&file->f_path, 0, 0);
}
SEC("lsm/file_mprotect")
int BPF_PROG(dir_mprotect, struct vm_area_struct *vma, unsigned long reqprot, unsigned long prot,
	     int ret)
{
	if (ret || !(prot & 2) || !(vma->vm_flags & 8))
		return ret;
	struct file *file = vma->vm_file;
	if (!file)
		return 0;
	return check(&file->f_path, 0, 0);
}
char LICENSE[] SEC("license") = "GPL";
