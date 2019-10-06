#!/usr/bin/env python3

d = bytes([0x47, 0x45, 0x54, 0x20, 0x2f, 0x20, 0x48, 0x54, 0x54, 0x50, 0x2f, 0x31, 0x2e,
           0x31, 0x0d, 0x0a, 0x68, 0x6f, 0x73, 0x74, 0x3a, 0x77, 0x77, 0x77, 0x2e, 0x67,
           0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x63, 0x6f, 0x6d, 0x3a, 0x34, 0x34, 0x33,
           0x0d, 0x0a, 0x78, 0x2d, 0x6e, 0x65, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f,
           0x2d, 0x66, 0x6f, 0x72, 0x3a, ord("1"), ord("2"), ord("7"), 0x2e, ord("0"), 0x2e, ord("0"), 0x2e, ord("1"), 0x0d,
           0x0a, 0x78, 0x2d, 0x6e, 0x65, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f, 0x2d,
           0x75, 0x75, 0x69, 0x64, 0x3a, 0x63, 0x61, 0x6e, 0x64, 0x79, 0x2e, 0x63, 0x6f,
           0x6d, 0x0d, 0x0a, 0x78, 0x2d, 0x6e, 0x65, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69,
           0x6f, 0x2d, 0x63, 0x6f, 0x64, 0x65, 0x63, 0x3a, 0x68, 0x74, 0x74, 0x70, 0x0d,
           0x0a, 0x78, 0x2d, 0x6e, 0x65, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f, 0x2d,
           0x73, 0x69, 0x64, 0x3a, 0x6d, 0x79, 0x73, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e,
           0x0d, 0x0a, 0x78, 0x2d, 0x6e, 0x65, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f,
           0x2d, 0x73, 0x72, 0x63, 0x70, 0x6f, 0x72, 0x74, 0x3a, 0x35, 0x36, 0x34, 0x32,
           0x39, 0x0d, 0x0a, 0x78, 0x2d, 0x6e, 0x65, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69,
           0x6f, 0x2d, 0x73, 0x72, 0x63, 0x69, 0x70, 0x3a, 0x3a, 0x3a, 0x66, 0x66, 0x66,
           0x66, 0x3a, 0x31, 0x32, 0x37, 0x2e, 0x30, 0x2e, 0x30, 0x2e, 0x31, 0x0d, 0x0a, 
           0x78, 0x2d, 0x6e, 0x65, 0x78, 0x74, 0x65, 0x6e, 0x73, 0x69, 0x6f, 0x2d, 0x6d, 
           0x73, 0x67, 0x74, 0x79, 0x70, 0x65, 0x3a, 0x54, 0x43, 0x50, 0x0d, 0x0a, 0x63, 
           0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x2d, 0x6c, 0x65, 0x6e, 0x67, 0x74, 0x68, 
           0x3a, 0x35, 0x31, 0x37, 0x0d, 0x0a, 0x0d, 0x0a, 0x16, 0x03, 0x01, 0x02, 0x00, 
           0x01, 0x00, 0x01, 0xfc, 0x03, 0x03, 0x3b, 0x40, 0x7c, 0x1a, 0x50, 0x4b, 0x3e, 
           0x4a, 0xac, 0xeb, 0x25, 0x1c, 0x04, 0xc2, 0x5f, 0x74, 0xb3, 0x74, 0x4f, 0x76, 
           0xd0, 0xe7, 0x4b, 0xc2, 0x82, 0xc1, 0xb5, 0xd4, 0x22, 0x5b, 0xc7, 0xe5, 0x20, 
           0x54, 0x6c, 0x09, 0x1c, 0x43, 0xd0, 0x5d, 0x3e, 0xfa, 0x87, 0x6c, 0xdc, 0xd2, 
           0x2d, 0x53, 0x9d, 0xb0, 0x5e, 0x98, 0x41, 0xf0, 0x84, 0x4b, 0x5e, 0x24, 0xd9, 
           0xf9, 0x00, 0xeb, 0xd1, 0x0c, 0x04, 0x00, 0x22, 0xba, 0xba, 0x13, 0x01, 0x13, 
           0x02, 0x13, 0x03, 0xc0, 0x2b, 0xc0, 0x2f, 0xc0, 0x2c, 0xc0, 0x30, 0xcc, 0xa9, 
           0xcc, 0xa8, 0xc0, 0x13, 0xc0, 0x14, 0x00, 0x9c, 0x00, 0x9d, 0x00, 0x2f, 0x00, 
           0x35, 0x00, 0x0a, 0x01, 0x00, 0x01, 0x91, 0x3a, 0x3a, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x13, 0x00, 0x11, 0x00, 0x00, 0x0e, 0x77, 0x77, 0x77, 0x2e, 0x67, 0x6f, 
           0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x63, 0x6f, 0x6d, 0x00, 0x17, 0x00, 0x00, 0xff, 
           0x01, 0x00, 0x01, 0x00, 0x00, 0x0a, 0x00, 0x0a, 0x00, 0x08, 0xaa, 0xaa, 0x00, 
           0x1d, 0x00, 0x17, 0x00, 0x18, 0x00, 0x0b, 0x00, 0x02, 0x01, 0x00, 0x00, 0x23, 
           0x00, 0x00, 0x00, 0x10, 0x00, 0x0e, 0x00, 0x0c, 0x02, 0x68, 0x32, 0x08, 0x68, 
           0x74, 0x74, 0x70, 0x2f, 0x31, 0x2e, 0x31, 0x00, 0x05, 0x00, 0x05, 0x01, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x0d, 0x00, 0x14, 0x00, 0x12, 0x04, 0x03, 0x08, 0x04, 
           0x04, 0x01, 0x05, 0x03, 0x08, 0x05, 0x05, 0x01, 0x08, 0x06, 0x06, 0x01, 0x02, 
           0x01, 0x00, 0x12, 0x00, 0x00, 0x00, 0x33, 0x00, 0x2b, 0x00, 0x29, 0xaa, 0xaa, 
           0x00, 0x01, 0x00, 0x00, 0x1d, 0x00, 0x20, 0xfa, 0xd5, 0xff, 0x31, 0x58, 0xea, 
           0xd5, 0x92, 0x8f, 0x23, 0x37, 0x7d, 0xf7, 0x0c, 0xe2, 0xae, 0x04, 0x47, 0xd0, 
           0x60, 0x79, 0xe0, 0x1e, 0x2c, 0x4b, 0xc4, 0xd7, 0x29, 0x07, 0x55, 0x36, 0x12, 
           0x00, 0x2d, 0x00, 0x02, 0x01, 0x01, 0x00, 0x2b, 0x00, 0x0b, 0x0a, 0x0a, 0x0a, 
           0x03, 0x04, 0x03, 0x03, 0x03, 0x02, 0x03, 0x01, 0x00, 0x1b, 0x00, 0x03, 0x02, 
           0x00, 0x02, 0x1a, 0x00, 0x1a, 0x00, 0x01, 0x00, 0x00, 0x15, 0x00, 0xca, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 
           0x00, 0x00, 0x00, 0x00, 0x00, 0x00])

if __name__ == '__main__':
    print(d)
    s = "".join(map(chr, d))
    print(s)
    print(s.encode('utf-8'))
