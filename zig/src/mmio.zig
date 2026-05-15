const std = @import("std");
const builtin = @import("builtin");

pub const CachePolicy = enum { Cached, Uncached, Streaming };

pub fn RawMMIOPtr(comptime T: type, comptime policy: CachePolicy) type {
    return struct {
        raw_ptr: *volatile T,
        pub inline fn write(self: @This(), value: T) void {
            switch (policy) {
                .Cached, .Uncached => self.raw_ptr.* = value,
                .Streaming => {
                    comptime if (T != @Vector(16, u8)) @compileError("Streaming requires @Vector(16, u8)");
                    if (builtin.cpu.arch != .x86_64) @compileError("Streaming supported only on x86_64");
                    asm volatile ("movntdq %[val], (%[dst])" :: [val] "x" (value), [dst] "r" (self.raw_ptr) : "memory");
                },
            }
        }
        pub inline fn read(self: @This()) T { return self.raw_ptr.*; }
    };
}

pub fn TxRing(comptime policy: CachePolicy) type {
    return struct {
        base_addr: usize,
        depth: u32,
        slot_stride: u32,
        tail: u64 = 0,
        next_sid: u32 = 0,
        const Self = @This();
        pub fn init(base: usize, depth: u32, stride: u32) Self {
            return .{ .base_addr = base, .depth = depth, .slot_stride = stride };
        }
        pub fn submitBatch(self: *Self, payloads: []const []const u8) u32 {
            const start_tail = self.tail;
            const start_sid = self.next_sid;
            for (payloads) |payload| {
                const index = @as(usize, @intCast(self.tail % self.depth));
                const slot_base = self.base_addr + index * self.slot_stride;
                const data_ptr: *volatile @Vector(16, u8) = @ptrFromInt(slot_base + 0x10);
                const mmio_data = RawMMIOPtr(@Vector(16, u8), policy){ .raw_ptr = data_ptr };
                var i: usize = 0;
                while (i + 16 <= payload.len) : (i += 16) {
                    const chunk: @Vector(16, u8) = payload[i..][0..16].*;
                    mmio_data.write(chunk);
                }
                const hdr_ptr: *volatile u32 = @ptrFromInt(slot_base + 0x04);
                const sid_len: u32 = (@as(u32, self.next_sid) << 16) | @as(u32, @truncate(payload.len));
                RawMMIOPtr(u32, .Uncached){ .raw_ptr = hdr_ptr }.write(sid_len);
                self.tail += 1;
                self.next_sid +%= 1;
            }
            if (policy == .Streaming) @fence(.seq_cst);
            var j: u64 = 0;
            while (j < payloads.len) : (j += 1) {
                const index = @as(usize, @intCast((start_tail + j) % self.depth));
                const status_ptr: *volatile u32 = @ptrFromInt(self.base_addr + index * self.slot_stride);
                RawMMIOPtr(u32, .Uncached){ .raw_ptr = status_ptr }.write(1);
            }
            const doorbell_ptr: *volatile u32 = @ptrFromInt(self.base_addr + 0x40);
            RawMMIOPtr(u32, .Uncached){ .raw_ptr = doorbell_ptr }.write(@as(u32, @truncate(self.tail)));
            return start_sid;
        }
    };
}

pub fn MultiLaneTxRing(comptime lanes: u8) type {
    return struct {
        rings: [lanes]TxRing(PROVISIONED_CACHE_POLICY),
        pub fn init(base_addrs: [lanes]usize, depth: u32, stride: u32) @This() {
            var self: @This() = undefined;
            for (0..lanes) |i| self.rings[i] = TxRing(PROVISIONED_CACHE_POLICY).init(base_addrs[i], depth, stride);
            return self;
        }
        pub fn submitBatch(self: *@This(), lane: u8, payloads: []const []const u8) u32 {
            return self.rings[lane].submitBatch(payloads);
        }
    };
}

pub const PROVISIONED_CACHE_POLICY: CachePolicy = .Streaming;
